package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
)

const (
	gmailScope    = "https://www.googleapis.com/auth/gmail.readonly"
	gmailAPIBase  = "https://gmail.googleapis.com/gmail/v1/users/me"
	defaultGmailMax = 50
)

var (
	htmlTagRE   = regexp.MustCompile(`(?s)<[^>]+>`)
	htmlSpaceRE = regexp.MustCompile(`\s+`)
)

// GmailConnector fetches messages from Gmail using a service account with
// domain-wide delegation (GOOGLE_IMPERSONATE_USER must be set).
type GmailConnector struct {
	auth *GoogleAuth
}

// NewGmailConnector creates a GmailConnector. auth must be non-nil.
func NewGmailConnector(auth *GoogleAuth) *GmailConnector {
	return &GmailConnector{auth: auth}
}

func (c *GmailConnector) Name() string { return "google_gmail" }

// Fetch retrieves Gmail messages matching opts.Query (Gmail search syntax).
// opts.MaxResults caps the fetch count; defaults to 50.
func (c *GmailConnector) Fetch(ctx context.Context, opts connectors.FetchOptions) ([]connectors.ConnectorDocument, error) {
	max := opts.MaxResults
	if max <= 0 {
		max = defaultGmailMax
	}

	ids, err := c.listMessageIDs(ctx, opts.Query, max)
	if err != nil {
		return nil, fmt.Errorf("gmail list: %w", err)
	}

	docs := make([]connectors.ConnectorDocument, 0, len(ids))
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return docs, ctx.Err()
		default:
		}
		doc, err := c.fetchMessage(ctx, id)
		if err != nil {
			continue // skip unreadable messages
		}
		if strings.TrimSpace(doc.Content) == "" {
			continue
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// gmailMessageList is the response from the messages.list API.
type gmailMessageList struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
	NextPageToken    string `json:"nextPageToken"`
	ResultSizeEstimate int  `json:"resultSizeEstimate"`
}

func (c *GmailConnector) listMessageIDs(ctx context.Context, query string, max int) ([]string, error) {
	var ids []string
	pageToken := ""
	for len(ids) < max {
		batch := max - len(ids)
		if batch > 500 {
			batch = 500
		}
		params := url.Values{
			"maxResults": {fmt.Sprintf("%d", batch)},
		}
		if strings.TrimSpace(query) != "" {
			params.Set("q", query)
		}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		endpoint := gmailAPIBase + "/messages?" + params.Encode()
		body, status, err := c.auth.doGet(endpoint, []string{gmailScope})
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("gmail messages.list status %d", status)
		}
		var list gmailMessageList
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("parsing messages.list: %w", err)
		}
		for _, m := range list.Messages {
			ids = append(ids, m.ID)
		}
		if list.NextPageToken == "" || len(list.Messages) == 0 {
			break
		}
		pageToken = list.NextPageToken
	}
	if len(ids) > max {
		ids = ids[:max]
	}
	return ids, nil
}

type gmailMessage struct {
	ID      string `json:"id"`
	Payload struct {
		Headers []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"headers"`
		MimeType string `json:"mimeType"`
		Body     struct {
			Data string `json:"data"`
		} `json:"body"`
		Parts []gmailPart `json:"parts"`
	} `json:"payload"`
}

type gmailPart struct {
	MimeType string `json:"mimeType"`
	Body     struct {
		Data string `json:"data"`
	} `json:"body"`
	Parts []gmailPart `json:"parts"`
}

func (c *GmailConnector) fetchMessage(ctx context.Context, id string) (connectors.ConnectorDocument, error) {
	endpoint := gmailAPIBase + "/messages/" + url.PathEscape(id) + "?format=full"
	body, status, err := c.auth.doGet(endpoint, []string{gmailScope})
	if err != nil {
		return connectors.ConnectorDocument{}, err
	}
	if status != http.StatusOK {
		return connectors.ConnectorDocument{}, fmt.Errorf("gmail messages.get status %d for id %s", status, id)
	}
	var msg gmailMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return connectors.ConnectorDocument{}, fmt.Errorf("parsing message %s: %w", id, err)
	}

	subject := headerValue(msg.Payload.Headers, "Subject")
	from := headerValue(msg.Payload.Headers, "From")
	date := headerValue(msg.Payload.Headers, "Date")

	content := extractGmailText(&msg.Payload.Parts, msg.Payload.MimeType, msg.Payload.Body.Data)

	sourceRef := "gmail://" + id
	title := subject
	if title == "" {
		title = "(no subject)"
	}

	return connectors.ConnectorDocument{
		ID:        id,
		Title:     title,
		Content:   content,
		SourceRef: sourceRef,
		Metadata: map[string]string{
			"source_type": "google_gmail",
			"source_ref":  sourceRef,
			"subject":     subject,
			"from":        from,
			"date":        date,
			"fetched_at":  time.Now().UTC().Format(time.RFC3339),
		},
	}, nil
}

// extractGmailText recursively walks MIME parts to find text content.
func extractGmailText(parts *[]gmailPart, mimeType, bodyData string) string {
	if parts != nil && len(*parts) > 0 {
		// Prefer text/plain, fall back to text/html
		var htmlContent string
		for _, part := range *parts {
			switch part.MimeType {
			case "text/plain":
				if data := decodeBase64URL(part.Body.Data); strings.TrimSpace(data) != "" {
					return data
				}
			case "text/html":
				htmlContent = stripHTML(decodeBase64URL(part.Body.Data))
			default:
				// recurse into multipart
				if strings.HasPrefix(part.MimeType, "multipart/") {
					if sub := extractGmailText(&part.Parts, part.MimeType, ""); sub != "" {
						return sub
					}
				}
			}
		}
		if htmlContent != "" {
			return htmlContent
		}
	}
	if bodyData != "" {
		raw := decodeBase64URL(bodyData)
		mt, _, _ := mime.ParseMediaType(mimeType)
		if mt == "text/html" {
			return stripHTML(raw)
		}
		return raw
	}
	return ""
}

func headerValue(headers []struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}, name string) string {
	for _, h := range headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func decodeBase64URL(s string) string {
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// try raw
		b, err = base64.RawStdEncoding.DecodeString(s)
		if err != nil {
			return ""
		}
	}
	return string(b)
}

func stripHTML(s string) string {
	s = htmlTagRE.ReplaceAllString(s, " ")
	s = htmlSpaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

var _ connectors.Connector = (*GmailConnector)(nil)
