package impexp

import (
	"encoding/base64"
	"fmt"
	"strings"

	commonv1 "github.com/testpilot/testpilot/gen/common/v1"
	"github.com/testpilot/testpilot/internal/model"
	"gorm.io/gorm"
)

// curlRequest 是 curl 命令的解析结果。
type curlRequest struct {
	Method  string
	URL     string
	Headers [][2]string
	Body    string
}

// tokenize 按 shell 习惯切分：支持单/双引号、反斜杠续行。
func tokenize(s string) ([]string, error) {
	s = strings.ReplaceAll(s, "\\\r\n", " ")
	s = strings.ReplaceAll(s, "\\\n", " ")
	var out []string
	var cur strings.Builder
	var quote rune // 0 = 无引号
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote == 0 && (c == '\'' || c == '"'):
			quote = c
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == ' ' || c == '\t'):
			flush()
		case quote == 0 && c == '\\' && i+1 < len(runes):
			i++
			cur.WriteRune(runes[i])
		default:
			cur.WriteRune(c)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return out, nil
}

// ParseCurl 解析 curl 命令行为请求结构。
func ParseCurl(command string) (*curlRequest, error) {
	tokens, err := tokenize(command)
	if err != nil {
		return nil, err
	}
	if len(tokens) > 0 && tokens[0] == "curl" {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty curl command")
	}
	req := &curlRequest{Method: ""}
	var dataParts []string
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		next := func() string {
			if i+1 < len(tokens) {
				i++
				return tokens[i]
			}
			return ""
		}
		switch {
		case t == "-X" || t == "--request":
			req.Method = strings.ToUpper(next())
		case strings.HasPrefix(t, "-X") && len(t) > 2:
			req.Method = strings.ToUpper(t[2:])
		case t == "-H" || t == "--header":
			if h := next(); h != "" {
				if k, v, ok := strings.Cut(h, ":"); ok {
					req.Headers = append(req.Headers, [2]string{strings.TrimSpace(k), strings.TrimSpace(v)})
				}
			}
		case t == "-d" || t == "--data" || t == "--data-raw" || t == "--data-binary" ||
			t == "--data-ascii" || t == "--data-urlencode" || t == "-F" || t == "--form":
			dataParts = append(dataParts, next())
		case t == "--url":
			req.URL = next()
		case t == "-u" || t == "--user":
			cred := next()
			req.Headers = append(req.Headers, [2]string{
				"Authorization", "Basic " + base64.StdEncoding.EncodeToString([]byte(cred))})
		case t == "-G" || t == "--get":
			if req.Method == "" {
				req.Method = "GET"
			}
		case t == "-k" || t == "--insecure" || t == "-s" || t == "-S" || t == "-i" ||
			t == "-v" || t == "-L" || t == "--location" || t == "--compressed":
			// 忽略行为类开关
		case strings.HasPrefix(t, "-"):
			// 未识别开关：若带值形式 --flag value 无法判定，按忽略处理
		default:
			if req.URL == "" {
				req.URL = t
			}
		}
	}
	if req.URL == "" {
		return nil, fmt.Errorf("no URL found in curl command")
	}
	req.Body = strings.Join(dataParts, "&")
	if req.Method == "" {
		if req.Body != "" {
			req.Method = "POST"
		} else {
			req.Method = "GET"
		}
	}
	return req, nil
}

// ImportCurl 解析 curl 命令并插入一条 HttpApi。
func ImportCurl(db *gorm.DB, tenantID, projectID int64, command string) (int64, error) {
	req, err := ParseCurl(command)
	if err != nil {
		return 0, err
	}
	m, ok := methodToEnum[strings.ToLower(req.Method)]
	if !ok {
		return 0, fmt.Errorf("unsupported method: %s", req.Method)
	}
	var body model.JSON
	if req.Body != "" {
		ct := commonv1.BodyContentType_BODY_CONTENT_TYPE_X_WWW_FORM_URLENCODED
		trimmed := strings.TrimSpace(req.Body)
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			ct = commonv1.BodyContentType_BODY_CONTENT_TYPE_JSON
		}
		body = bodyJSON(ct, req.Body)
	}
	created, id, err := insertAPI(db, tenantID, projectID, m, req.URL,
		kvJSON(req.Headers), nil, body)
	if err != nil {
		return 0, err
	}
	if !created {
		return id, fmt.Errorf("api already exists (id=%d)", id)
	}
	return id, nil
}
