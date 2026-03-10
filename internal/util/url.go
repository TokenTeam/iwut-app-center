package util

import (
	"net/url"
	"strings"
)

func IsHttpURL(str string) bool {
	// 快速检查是否以http://或https://开头
	if !strings.HasPrefix(str, "http://") && !strings.HasPrefix(str, "https://") {
		return false
	}

	// 使用url.Parse进行完整验证
	parsed, err := url.Parse(str)
	if err != nil {
		return false
	}

	// 确保有有效的主机名
	if parsed.Host == "" {
		return false
	}

	// 确保是http或https协议
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}

	return true
}

func BuildInviteUrl(baseURL string, params map[string]string) (string, error) {
	if baseURL == "" {
		return "", nil
	}

	// 记录原始字符串是否以 '/' 结尾（用于决定是否保留单个根路径 '/'）
	origEndsWithSlash := strings.HasSuffix(baseURL, "/")

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}

	// 规范化 path：
	// - 如果 path 为空但原始 baseURL 以 '/' 结尾，保留单个 '/'（例如 "https://a.com/"）
	// - 否则去掉 path 末尾多余的 '/'（但不要把单个 '/' 去掉）
	if u.Path == "" {
		if origEndsWithSlash {
			u.Path = "/"
		}
	} else {
		if u.Path != "/" {
			trimmed := strings.TrimRight(u.Path, "/")
			if trimmed == "" && origEndsWithSlash {
				// 原 path 由多重 '/' 组成且原始字符串以 '/' 结尾，保留单个 '/'
				u.Path = "/"
			} else {
				u.Path = trimmed
			}
		}
	}

	q := u.Query()
	for k, v := range params {
		if v == "" {
			continue
		}
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
