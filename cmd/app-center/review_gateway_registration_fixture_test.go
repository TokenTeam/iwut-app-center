package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	authentication "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v1/authentication"
	emailaccountv3 "github.com/TokenTeam/iwut-api-proto/gen/go/auth_center/v3/email_account"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// registerReviewManager uses public Auth protocols. Auth owns all principal,
// verified email directory, login binding and device Session writes.
func registerReviewManager(t *testing.T, ctx context.Context) (string, string) {
	t.Helper()
	email := "uc005-manager-" + uuid.NewString() + "@example.test"
	mail := captureReviewRegistrationMail(t, email)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient(os.Getenv("AUTH_CENTER_INTEGRATION_TARGET"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	accounts := emailaccountv3.NewEmailAccountServiceClient(conn)
	started, err := accounts.BeginEmailRegistration(ctx, &emailaccountv3.BeginEmailRegistrationRequest{
		RequestId: uuid.NewString(), Email: email,
		CredentialProposal: &authentication.CredentialRegistrationProposal{
			ProtocolVersion: "iwut-device-v1", PublicKey: elliptic.Marshal(key.Curve, key.X, key.Y),
		},
	})
	if err != nil {
		t.Fatal("formal email registration begin failed", status.Code(err))
	}
	if uuid.Validate(started.OperationId) != nil || started.TargetEmail != email || started.ProtocolVersion != "iwut-email-registration-v3" || len(started.SigningPayload) == 0 || started.ExpiresAtUnixMs <= time.Now().UnixMilli() {
		t.Fatal("invalid email registration challenge")
	}
	var code string
	select {
	case result := <-mail:
		if result.err != nil {
			t.Fatal("controlled SMTP delivery failed", result.err)
		}
		code = result.code
	case <-ctx.Done():
		t.Fatal("email registration delivery deadline exceeded")
	}
	proof := func(payload []byte) *authentication.CredentialProof {
		t.Helper()
		digest := sha256.Sum256(payload)
		signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
		if err != nil {
			t.Fatal(err)
		}
		return &authentication.CredentialProof{Signature: signature}
	}
	// Completion and bootstrap are deliberately single attempts: an unknown
	// outcome must stop acceptance rather than replay an authority mutation.
	completed, err := accounts.CompleteEmailRegistration(ctx, &emailaccountv3.CompleteEmailRegistrationRequest{
		OperationId: started.OperationId, Code: code, Password: "Uc005!" + uuid.NewString() + uuid.NewString(), Proof: proof(started.SigningPayload),
	})
	if err != nil {
		t.Fatal("formal email registration completion failed", status.Code(err))
	}
	registered := completed.GetRegistrationResult()
	if completed.OperationId != started.OperationId || registered.GetAuthId() == "" || registered.GetCredentialId() == "" {
		t.Fatal("invalid email registration acknowledgment")
	}
	validateSession := func(session *authentication.SessionEstablished) {
		t.Helper()
		if session.GetAuthId() != registered.AuthId || session.GetCredentialId() != registered.CredentialId || session.GetSessionId() == "" || session.GetSessionToken() == "" || session.GetExpiresAtUnixMs() <= time.Now().UnixMilli() {
			t.Fatal("invalid registered device Session")
		}
	}
	validateSession(registered.Session)
	devices := authentication.NewAuthenticationServiceClient(conn)
	challenge, err := devices.BeginDeviceLogin(ctx, &authentication.BeginDeviceLoginRequest{
		CredentialLocator: &authentication.BeginDeviceLoginRequest_CredentialId{CredentialId: registered.CredentialId},
	})
	if err != nil {
		t.Fatal("registered device login begin failed", status.Code(err))
	}
	if challenge.ProtocolVersion != "iwut-device-v1" || len(challenge.SigningPayload) == 0 || challenge.ExpiresAtUnixMs <= time.Now().UnixMilli() || uuid.Validate(challenge.OperationId) != nil {
		t.Fatal("invalid device login challenge")
	}
	session, err := devices.CompleteDeviceLogin(ctx, &authentication.CompleteDeviceLoginRequest{OperationId: challenge.OperationId, Proof: proof(challenge.SigningPayload)})
	if err != nil {
		t.Fatal("registered device login completion failed", status.Code(err))
	}
	validateSession(session)
	own, err := accounts.GetOwnEmails(metadata.NewOutgoingContext(ctx, metadata.Pairs("x-iwut-session", session.SessionToken)), &emailaccountv3.GetOwnEmailsRequest{})
	if err != nil {
		t.Fatal("registered email directory read failed", status.Code(err))
	}
	if own.DirectoryRevision < 1 || own.LoginRoleRevision < 1 || len(own.Emails) != 1 || own.Emails[0].Email != email || !own.Emails[0].IsLogin || own.Emails[0].BindingId == "" || own.Emails[0].VerifiedAtUnixMs <= 0 {
		t.Fatal("formal registration did not establish a verified login email")
	}
	t.Log("UC005 prerequisite passed: formal email registration, device login and verified login directory")
	return registered.AuthId, session.SessionToken
}

type reviewRegistrationMail struct {
	code string
	err  error
}

// One message, one expected recipient, bounded input and no credential logging.
// The listener exists only during this test's formal manager registration.
func captureReviewRegistrationMail(t *testing.T, email string) <-chan reviewRegistrationMail {
	t.Helper()
	cert, err := tls.LoadX509KeyPair(os.Getenv("UC002_SMTP_CERT_FILE"), os.Getenv("UC002_SMTP_KEY_FILE"))
	if err != nil {
		t.Fatal("load controlled SMTP TLS material", err)
	}
	listener, err := tls.Listen("tcp", os.Getenv("UC002_SMTP_ADDR"), &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal("start controlled SMTP listener", err)
	}
	result := make(chan reviewRegistrationMail, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			t.Error("controlled SMTP shutdown timed out")
		}
	})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			result <- reviewRegistrationMail{err: fmt.Errorf("SMTP accept failed")}
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		code, err := receiveReviewRegistrationMail(conn, email)
		result <- reviewRegistrationMail{code: code, err: err}
	}()
	return result
}

func receiveReviewRegistrationMail(conn net.Conn, email string) (string, error) {
	reader := textproto.NewReader(bufio.NewReader(io.LimitReader(conn, 64<<10)))
	write := func(line string) error { _, err := io.WriteString(conn, line+"\r\n"); return err }
	if err := write("220 localhost ESMTP"); err != nil {
		return "", err
	}
	sender, recipient, code := false, false, ""
	for {
		line, err := reader.ReadLine()
		if err != nil {
			return "", fmt.Errorf("SMTP command read failed")
		}
		var reply string
		switch {
		case strings.HasPrefix(line, "EHLO "), strings.HasPrefix(line, "HELO "):
			reply = "250 localhost"
		case line == "MAIL FROM:<auth@example.test>":
			sender = true
			reply = "250 OK"
		case sender && line == "RCPT TO:<"+email+">":
			recipient = true
			reply = "250 OK"
		case recipient && line == "DATA":
			if err := write("354 End with dot"); err != nil {
				return "", err
			}
			body, err := reader.ReadDotBytes()
			if err != nil {
				return "", fmt.Errorf("SMTP bounded message read failed")
			}
			code, err = reviewRegistrationCode(string(body))
			if err != nil {
				return "", err
			}
			reply = "250 OK"
		case line == "QUIT" && code != "":
			return code, write("221 Bye")
		default:
			_ = write("550 Unexpected fixture SMTP command")
			return "", fmt.Errorf("unexpected fixture SMTP command")
		}
		if err := write(reply); err != nil {
			return "", err
		}
	}
}

var reviewRegistrationCodePattern = regexp.MustCompile(`(?m)^Verification code: ([0-9]{8})\r?$`)

func reviewRegistrationCode(body string) (string, error) {
	codes := reviewRegistrationCodePattern.FindAllStringSubmatch(body, -1)
	if len(codes) != 1 {
		return "", fmt.Errorf("expected exactly one registration code")
	}
	return codes[0][1], nil
}

func TestReviewRegistrationCode(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"delivered", "Subject: verification\r\n\r\nVerification code: 12345678\r\n", true},
		{"missing", "No verification code", false},
		{"short", "Verification code: 1234567\n", false},
		{"long", "Verification code: 123456789\n", false},
		{"ambiguous", "Verification code: 12345678\nVerification code: 87654321\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, err := reviewRegistrationCode(tc.body)
			if (err == nil) != tc.valid || (tc.valid && code != "12345678") {
				t.Fatal("unexpected registration code parsing result")
			}
		})
	}
}
