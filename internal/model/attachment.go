package model

import "strings"

type Attachment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Size      int    `json:"size"`
}

type AgentAttachment struct {
	Attachment
	Path string
}

func (a Attachment) Image() bool { return strings.HasPrefix(a.MediaType, "image/") }
