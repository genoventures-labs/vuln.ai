package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
)

const (
	driveScope   = "https://www.googleapis.com/auth/drive.readonly"
	driveAPIBase = "https://www.googleapis.com/drive/v3"

	mimeGoogleDoc         = "application/vnd.google-apps.document"
	mimeGoogleSheet       = "application/vnd.google-apps.spreadsheet"
	mimeGoogleSlides      = "application/vnd.google-apps.presentation"
	mimeGoogleFolder      = "application/vnd.google-apps.folder"
	defaultDriveMax       = 100
	drivePageSize         = 100
)

// DriveConnector fetches files from Google Drive, including Google Docs, Sheets,
// and Slides (exported as plain text) and regular downloadable files.
type DriveConnector struct {
	auth *GoogleAuth
}

// NewDriveConnector creates a DriveConnector. auth must be non-nil.
func NewDriveConnector(auth *GoogleAuth) *DriveConnector {
	return &DriveConnector{auth: auth}
}

func (c *DriveConnector) Name() string { return "google_drive" }

// Fetch lists and downloads files from Drive.
// opts.FolderID restricts to a specific folder; opts.Query applies a Drive search query.
// opts.MaxResults caps the total files fetched.
func (c *DriveConnector) Fetch(ctx context.Context, opts connectors.FetchOptions) ([]connectors.ConnectorDocument, error) {
	max := opts.MaxResults
	if max <= 0 {
		max = defaultDriveMax
	}

	files, err := c.listFiles(ctx, opts.FolderID, opts.Query, max)
	if err != nil {
		return nil, fmt.Errorf("drive list: %w", err)
	}

	docs := make([]connectors.ConnectorDocument, 0, len(files))
	for _, f := range files {
		select {
		case <-ctx.Done():
			return docs, ctx.Err()
		default:
		}
		doc, err := c.downloadFile(ctx, f)
		if err != nil {
			continue
		}
		if strings.TrimSpace(doc.Content) == "" {
			continue
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

type driveFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	WebViewLink string `json:"webViewLink"`
	ModifiedTime string `json:"modifiedTime"`
}

type driveFileList struct {
	Files         []driveFile `json:"files"`
	NextPageToken string      `json:"nextPageToken"`
}

func (c *DriveConnector) listFiles(ctx context.Context, folderID, query string, max int) ([]driveFile, error) {
	var files []driveFile
	pageToken := ""

	for len(files) < max {
		params := url.Values{
			"pageSize": {fmt.Sprintf("%d", min(drivePageSize, max-len(files)))},
			"fields":   {"files(id,name,mimeType,webViewLink,modifiedTime),nextPageToken"},
		}

		// Build query — skip folders
		parts := []string{"mimeType != '" + mimeGoogleFolder + "'", "trashed = false"}
		if folderID != "" {
			parts = append(parts, fmt.Sprintf("'%s' in parents", folderID))
		}
		if query != "" {
			parts = append(parts, query)
		}
		params.Set("q", strings.Join(parts, " and "))

		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}

		endpoint := driveAPIBase + "/files?" + params.Encode()
		body, status, err := c.auth.doGet(endpoint, []string{driveScope})
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("drive files.list status %d", status)
		}

		var list driveFileList
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, fmt.Errorf("parsing files.list: %w", err)
		}
		files = append(files, list.Files...)
		if list.NextPageToken == "" || len(list.Files) == 0 {
			break
		}
		pageToken = list.NextPageToken
	}

	if len(files) > max {
		files = files[:max]
	}
	return files, nil
}

func (c *DriveConnector) downloadFile(ctx context.Context, f driveFile) (connectors.ConnectorDocument, error) {
	var content string
	var err error

	switch f.MimeType {
	case mimeGoogleDoc:
		content, err = c.exportFile(f.ID, "text/plain")
	case mimeGoogleSheet:
		content, err = c.exportFile(f.ID, "text/csv")
	case mimeGoogleSlides:
		content, err = c.exportFile(f.ID, "text/plain")
	default:
		// Download regular files up to 10 MB
		if isTextMIME(f.MimeType) {
			content, err = c.downloadMedia(f.ID)
		} else {
			return connectors.ConnectorDocument{}, fmt.Errorf("skipping binary file %s (%s)", f.Name, f.MimeType)
		}
	}
	if err != nil {
		return connectors.ConnectorDocument{}, err
	}

	sourceRef := f.WebViewLink
	if sourceRef == "" {
		sourceRef = "https://drive.google.com/file/d/" + f.ID
	}

	return connectors.ConnectorDocument{
		ID:        f.ID,
		Title:     f.Name,
		Content:   content,
		SourceRef: sourceRef,
		Metadata: map[string]string{
			"source_type":   "google_drive",
			"source_ref":    sourceRef,
			"file_name":     f.Name,
			"mime_type":     f.MimeType,
			"modified_time": f.ModifiedTime,
			"fetched_at":    time.Now().UTC().Format(time.RFC3339),
		},
	}, nil
}

func (c *DriveConnector) exportFile(fileID, mimeType string) (string, error) {
	endpoint := driveAPIBase + "/files/" + url.PathEscape(fileID) +
		"/export?mimeType=" + url.QueryEscape(mimeType)
	body, status, err := c.auth.doGet(endpoint, []string{driveScope})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("drive export status %d for file %s", status, fileID)
	}
	return strings.TrimSpace(string(body)), nil
}

func (c *DriveConnector) downloadMedia(fileID string) (string, error) {
	endpoint := driveAPIBase + "/files/" + url.PathEscape(fileID) + "?alt=media"

	token, err := c.auth.Token(driveScope)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.auth.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("drive download %s: %w", fileID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("drive download status %d for file %s", resp.StatusCode, fileID)
	}
	// Cap at 10 MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return "", fmt.Errorf("reading drive file %s: %w", fileID, err)
	}
	return strings.TrimSpace(string(body)), nil
}

func isTextMIME(mime string) bool {
	return strings.HasPrefix(mime, "text/") ||
		mime == "application/json" ||
		mime == "application/xml" ||
		mime == "application/x-yaml"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ connectors.Connector = (*DriveConnector)(nil)
