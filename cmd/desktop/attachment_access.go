package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"

	chatmodel "spool/internal/model"
)

const desktopFileAccessPath = "/__desktop/file-access"

type attachmentAccessTarget struct {
	ChatID string `json:"chat_id"`
	Path   string `json:"path"`
	Error  string `json:"error,omitempty"`
}

var attachmentAccessResult = mygo.NewEvent[attachmentAccessTarget]("spool:file-access-result")

func attachmentAccessNavigation(address, target string) (attachmentAccessTarget, bool) {
	if !chatNavigationAllowed(address, target) {
		return attachmentAccessTarget{}, false
	}
	u, err := url.Parse(target)
	if err != nil || u.Path != desktopFileAccessPath || u.RawPath != "" || u.Fragment != "" {
		return attachmentAccessTarget{}, false
	}
	query, err := url.ParseQuery(u.RawQuery)
	path, chat := query.Get("path"), query.Get("chat")
	if err != nil || len(query) != 2 || len(query["chat"]) != 1 || len(query["path"]) != 1 || !chatmodel.ValidID(chat) || !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return attachmentAccessTarget{}, false
	}
	return attachmentAccessTarget{ChatID: chat, Path: path}, true
}

func selectedAttachmentPath(expected string, paths []string) bool {
	return len(paths) == 1 && filepath.Clean(paths[0]) == filepath.Clean(expected)
}

func (p *panel) chooseAttachmentAccess(target attachmentAccessTarget) {
	if p.fileAccessPending || p.quitting || p.chatView == nil {
		return
	}
	p.fileAccessPending = true
	page := p.chatView.Page()
	address := p.chatURL
	parent := p.window
	title := p.tr("Allow access to attached file")
	message := p.tr("Select this attached file to allow Spool to read it.")
	button := p.tr("Allow access")
	go func() {
		var token string
		cookies, err := p.desktopSessionCookies(address)
		if err == nil && len(cookies) == 1 && cookies[0].HTTPOnly {
			token = cookies[0].Value
		}
		if token == "" {
			err = os.ErrPermission
		} else {
			_, err = p.service.UserAttachmentPath(token, target.ChatID, target.Path)
		}
		var paths []string
		if err == nil {
			paths, err = mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: parent, Title: title, Message: message, ButtonLabel: button, DefaultPath: target.Path})
			if err == nil && len(paths) > 0 && !selectedAttachmentPath(target.Path, paths) {
				err = os.ErrPermission
			}
		}
		if err == nil && len(paths) > 0 {
			cookies, err = p.desktopSessionCookies(address)
			if err == nil && (len(cookies) != 1 || !cookies[0].HTTPOnly || cookies[0].Value != token) {
				err = os.ErrPermission
			}
			if err == nil {
				_, err = p.service.UserAttachmentPath(token, target.ChatID, target.Path)
			}
			if err == nil {
				var file *os.File
				file, err = os.Open(target.Path)
				if file != nil {
					file.Close()
				}
			}
		}
		p.window.Update(func() {
			p.fileAccessPending = false
			if p.quitting || p.chatView == nil || p.chatView.Page() != page || p.chatURL != address || err == nil && len(paths) == 0 {
				return
			}
			if err != nil {
				target.Error = p.tr("Could not access the selected attachment. Check this app's file permissions on the service computer.")
			}
			_ = attachmentAccessResult.EmitPage(page, target)
		})
	}()
}
