package mongo

import (
	"go.mongodb.org/mongo-driver/v2/bson"
	profiledomain "iwut-app-center/internal/profile/domain"
	profileport "iwut-app-center/internal/profile/port"
	"iwut-app-center/internal/shared"
	"time"
)

type applicationProfileRevisionDocument struct {
	ProfileRevisionID string    `bson:"profileRevisionId"`
	ApplicationID     string    `bson:"applicationId"`
	Sequence          int32     `bson:"sequence"`
	DisplayName       string    `bson:"displayName"`
	Description       *string   `bson:"description"`
	Icon              *string   `bson:"icon"`
	ReviewStatus      string    `bson:"reviewStatus"`
	CreatedBy         string    `bson:"createdBy"`
	CreatedAt         time.Time `bson:"createdAt"`
	Revision          int64     `bson:"revision"`
	UpdatedBy         string    `bson:"updatedBy"`
	UpdatedAt         time.Time `bson:"updatedAt"`
}
type applicationProfileDocument struct {
	ApplicationID                     string  `bson:"applicationId"`
	WorkingProfileRevisionID          *string `bson:"workingProfileRevisionId"`
	CurrentPublishedProfileRevisionID *string `bson:"currentPublishedProfileRevisionId"`
}

func profileRevisionToDocument(revision *profiledomain.ApplicationProfileRevision) applicationProfileRevisionDocument {
	var description, icon *string
	if value := revision.Description(); value != nil {
		s := value.String()
		description = &s
	}
	if value := revision.Icon(); value != nil {
		s := value.String()
		icon = &s
	}
	return applicationProfileRevisionDocument{
		ProfileRevisionID: revision.ProfileRevisionID().String(), ApplicationID: revision.ApplicationID().String(), Sequence: int32(revision.Sequence()),
		DisplayName: revision.DisplayName().String(), Description: description, Icon: icon, ReviewStatus: string(revision.ReviewStatus()),
		CreatedBy: revision.CreatedBy().String(), CreatedAt: revision.CreatedAt().UTC().Truncate(time.Millisecond), Revision: revision.Revision(),
		UpdatedBy: revision.UpdatedBy().String(), UpdatedAt: revision.UpdatedAt().UTC().Truncate(time.Millisecond),
	}
}
func profileRevisionFromDocument(doc applicationProfileRevisionDocument) (*profiledomain.ApplicationProfileRevision, error) {
	revision, err := profiledomain.RestoreApplicationProfileRevision(profiledomain.ApplicationProfileRevisionState{
		ProfileRevisionID: profiledomain.ApplicationProfileRevisionID(doc.ProfileRevisionID), ApplicationID: shared.ApplicationID(doc.ApplicationID), Sequence: profiledomain.ProfileSequence(doc.Sequence),
		DisplayName: doc.DisplayName, Description: doc.Description, Icon: doc.Icon, ReviewStatus: profiledomain.ReviewStatus(doc.ReviewStatus), CreatedBy: shared.AuthID(doc.CreatedBy), CreatedAt: doc.CreatedAt,
		Revision: doc.Revision, UpdatedBy: shared.AuthID(doc.UpdatedBy), UpdatedAt: doc.UpdatedAt,
	})
	if err != nil {
		return nil, profileport.ErrApplicationProfileStateInconsistent
	}
	return revision, nil
}
func profileRevisionFromRaw(raw bson.Raw) (*profiledomain.ApplicationProfileRevision, error) {
	for _, name := range []string{"profileRevisionId", "applicationId", "displayName", "reviewStatus", "createdBy", "updatedBy"} {
		if raw.Lookup(name).Type != bson.TypeString {
			return nil, profileport.ErrApplicationProfileStateInconsistent
		}
	}
	for _, name := range []string{"description", "icon"} {
		kind := raw.Lookup(name).Type
		if kind != bson.TypeString && kind != bson.TypeNull {
			return nil, profileport.ErrApplicationProfileStateInconsistent
		}
	}
	if raw.Lookup("sequence").Type != bson.TypeInt32 || raw.Lookup("revision").Type != bson.TypeInt64 || raw.Lookup("createdAt").Type != bson.TypeDateTime || raw.Lookup("updatedAt").Type != bson.TypeDateTime {
		return nil, profileport.ErrApplicationProfileStateInconsistent
	}
	var doc applicationProfileRevisionDocument
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return nil, profileport.ErrApplicationProfileStateInconsistent
	}
	return profileRevisionFromDocument(doc)
}
func profileFromRaw(raw bson.Raw) (applicationProfileDocument, error) {
	var doc applicationProfileDocument
	if raw.Lookup("applicationId").Type != bson.TypeString {
		return doc, profileport.ErrApplicationProfileStateInconsistent
	}
	for _, name := range []string{"workingProfileRevisionId", "currentPublishedProfileRevisionId"} {
		kind := raw.Lookup(name).Type
		if kind != bson.TypeString && kind != bson.TypeNull {
			return doc, profileport.ErrApplicationProfileStateInconsistent
		}
	}
	if err := bson.Unmarshal(raw, &doc); err != nil {
		return doc, profileport.ErrApplicationProfileStateInconsistent
	}
	if !shared.ApplicationID(doc.ApplicationID).IsValid() {
		return doc, profileport.ErrApplicationProfileStateInconsistent
	}
	for _, id := range []*string{doc.WorkingProfileRevisionID, doc.CurrentPublishedProfileRevisionID} {
		if id != nil && !profiledomain.ApplicationProfileRevisionID(*id).IsValid() {
			return doc, profileport.ErrApplicationProfileStateInconsistent
		}
	}
	return doc, nil
}
