package spoolnet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"time"
)

// Upload accepts only the same bounded attachment payload as the web composer.
func (c *Client) Upload(chatID, payload, csrf, language string) (string, error) {
	if !chatIDPattern.MatchString(chatID) {
		return "", errors.New("invalid chat")
	}
	var input struct {
		Fields map[string]string `json:"fields"`
		Files  []struct {
			Name string `json:"name"`
			Data string `json:"data"`
		} `json:"files"`
	}
	if len(payload) > 24<<20 || json.Unmarshal([]byte(payload), &input) != nil || len(input.Files) > 6 {
		return "", errors.New("invalid attachments")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range input.Fields {
		switch key {
		case "message", "backend", "model", "reasoning_effort", "permission_mode":
		default:
			return "", errors.New("invalid message field")
		}
		if err := writer.WriteField(key, value); err != nil {
			return "", err
		}
	}
	total := 0
	for _, file := range input.Files {
		data, err := base64.StdEncoding.DecodeString(file.Data)
		total += len(data)
		if err != nil || len(data) == 0 || len(data) > 4<<20 || total > 16<<20 || file.Name == "" {
			return "", errors.New("attach at most 6 files, 4 MB each and 16 MB total")
		}
		part, err := writer.CreateFormFile("files", file.Name)
		if err != nil {
			return "", err
		}
		if _, err := part.Write(data); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(c.ctx, 60*time.Second)
	defer cancel()
	resp, err := c.request(ctx, http.MethodPost, "/api/chats/"+chatID+"/messages", &body, writer.FormDataContentType(), csrf, language)
	if err != nil {
		return "", err
	}
	return encodeResponse(resp)
}
