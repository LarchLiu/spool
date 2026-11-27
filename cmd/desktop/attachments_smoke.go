package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"spool"
)

// Exercise DOM drops and the native callback with real files in the system
// WebView. This does not automate a Finder/Explorer gesture.
func (p *panel) smokeAttachmentsOnly(report string, result chan<- error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := p.probeAttachmentDrop(ctx, report)
	if err == nil && runtime.GOOS == "darwin" {
		err = p.probeScreenshotSelection(ctx, report)
	}
	if err == nil {
		body, _ := json.Marshal(map[string]any{
			"scope": "attachments", "native": true, "target": runtime.GOOS + "-" + runtime.GOARCH,
			"dom_drop": true, "native_file_delivery": true, "duplicate_suppressed": true,
			"page_hover": true, "native_hover_bridge": true, "hover_exit": true,
			"image_preview": true, "submission_bytes": true, "os_drag_gesture": false,
			"screenshot_surface": runtime.GOOS == "darwin", "screenshot_navigation": runtime.GOOS == "darwin",
			"system_screen_capture":        false,
			"screenshot_visibility":        runtime.GOOS == "darwin",
			"screenshot_native_mouse":      runtime.GOOS == "darwin",
			"screenshot_display_alignment": runtime.GOOS == "darwin",
			"screenshot_edited_export":     runtime.GOOS == "darwin",
			"screenshot_export_authority":  runtime.GOOS == "darwin",
		})
		err = os.WriteFile(report, body, 0o600)
	}
	result <- err
	mygo.App.Quit()
}

func (p *panel) probeAttachmentDrop(ctx context.Context, report string) error {
	var view *mygo.WebView
	if err := p.rendererSmokeUI(ctx, func() error { view = p.chatView; return nil }); err != nil {
		return err
	}
	if view == nil {
		return fmt.Errorf("missing native chat WebView")
	}
	dir, err := os.MkdirTemp("", "attachment-drop-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		return err
	}
	paths := []string{filepath.Join(dir, "native-notes.txt"), filepath.Join(dir, "native-image.png")}
	for i, data := range [][]byte{[]byte("Native callback file bytes"), png} {
		if err := os.WriteFile(paths[i], data, 0o600); err != nil {
			return err
		}
	}
	address, err := p.service.LocalWebURL()
	if err != nil {
		return err
	}
	html, err := fs.ReadFile(spool.Frontend(), "frontend/index.html")
	if err != nil {
		return err
	}
	// Mock only service responses; load the real packaged HTML, JS and CSS into
	// the real native page. No installed account, provider or database is used.
	fixture := `<script>
window.spoolDesktop = true;
window.WebSocket = class extends EventTarget { readyState = 0; send() {} close() { this.readyState = 3; } };
const chat = {id:'attachment-chat',title:'Attachment drop',project_id:'attachment-project',user_id:'fixture',backend:'codex',status:'completed',updated_at:1,permission_mode:'ask',can_control:true,turns:[],transcript:{parts:[]}};
const originalFetch = window.fetch.bind(window);
window.fetch = async (input, options = {}) => {
 const path = new URL(typeof input === 'string' ? input : input.url, location.href).pathname;
 if (!path.startsWith('/api/')) return originalFetch(input, options);
 let data = {path:'.',entries:[],truncated:false};
 if (path === '/api/me') data = {user:{id:'fixture',nickname:'Fixture'},csrf:'attachment-csrf'};
 else if (path === '/api/version') data = {version:'fixture'};
 else if (path === '/api/navigation') data = {projects:[{id:'attachment-project',name:'Attachment project'}],chats:[chat]};
 else if (path === '/api/profiles') data = {revision:1,people:[{id:'fixture',nickname:'Fixture'}]};
 else if (path === '/api/agents') data = [{backend:'codex',enabled:true,installed:true,catalog:{models:[],commands:[]}}];
 else if (path === '/api/permissions') data = {backend:'codex',default:'ask',modes:[{id:'ask',available:true}],commands:[]};
 else if (path === '/api/federation/agents') data = [];
 else if (path.endsWith('/events')) return new Response('event: snapshot\ndata: '+JSON.stringify(chat)+'\n\n',{headers:{'Content-Type':'text/event-stream'}});
 else if (path.endsWith('/workspace')) data = {path:'/fixture',kind:'main',available:true};
 else if (path.endsWith('/messages')) {
  window.attachmentSubmission = {message:options.body.get('message'),files:await Promise.all(options.body.getAll('files').map(async file => ({name:file.name,type:file.type,size:file.size,bytes:Array.from(new Uint8Array(await file.arrayBuffer()))})))};
  data = chat;
 } else if (path === '/api/chats/attachment-chat') data = chat;
 return Response.json(data);
};
</script>`
	view.Page().LoadHTML(strings.Replace(string(html), "<head>", "<head>"+fixture, 1), address)
	page := view.Page()
	if err := waitChatScript(ctx, page, `return document.querySelector('#chat-message')?.disabled === false;`); err != nil {
		return err
	}
	// Release runners may leave the OS window hidden, pausing requestAnimationFrame.
	// Once the fixture DOM is committed, send its ordinary ready navigation so
	// the native selection guard presents it before exercising file callbacks.
	if _, err := page.EvalContext(ctx, `location.assign('/__desktop/server-ready?' + new URLSearchParams({state:'connected',selection:window.spoolServers.selection,authenticated:'true'}));`); err != nil {
		return err
	}
	if err := p.waitDesktopServerUI(ctx, func() bool {
		return p.tab == "Chat" && p.chatView == view && p.chatPresentedView == view && !p.serverDialogOpen
	}); err != nil {
		return fmt.Errorf("attachment fixture presentation: %w", err)
	}
	// Cross the native event/preload boundary with the pointer over history,
	// outside the composer's bounds. No DOM composer drag event precedes this.
	if err := p.rendererSmokeUI(ctx, func() error {
		return desktopAttachmentDragged.EmitPage(page, desktopAttachmentDrag{Active: true, X: 400, Y: 160})
	}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return !!document.querySelector('.composer-drop-hint') && getComputedStyle(document.querySelector('.history')).opacity === '1';`); err != nil {
		return fmt.Errorf("native page hover: %w", err)
	}
	image, err := view.CapturePage()
	if err != nil {
		return err
	}
	if err := os.WriteFile(report+"-page-hover.png", image, 0o600); err != nil {
		return err
	}
	if err := p.rendererSmokeUI(ctx, func() error {
		return desktopAttachmentDragged.EmitPage(page, desktopAttachmentDrag{})
	}); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return !document.querySelector('.composer-drop-hint') && getComputedStyle(document.querySelector('.history')).opacity === '1';`); err != nil {
		return fmt.Errorf("native hover exit: %w", err)
	}
	if _, err := page.EvalContext(ctx, `
 const input = document.querySelector('#chat-message');
 Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(input,'Native desktop draft');
 input.dispatchEvent(new Event('input',{bubbles:true}));
 window.attachmentDrag = new DataTransfer();
 window.attachmentDrag.items.add(new File(['Native file bytes'],'notes.txt',{type:'text/plain'}));
 window.attachmentDrag.items.add(new File([Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII='),v=>v.charCodeAt(0))],'image.png',{type:'image/png'}));
 const rect = input.getBoundingClientRect();
 window.attachmentPoint = {clientX:Math.round(rect.x + rect.width/2),clientY:Math.round(rect.y + rect.height/2)};
 const event = new DragEvent('dragenter',{bubbles:true,cancelable:true,dataTransfer:window.attachmentDrag});
 document.querySelector('.history').dispatchEvent(event);
 `); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return !!document.querySelector('.composer-drop-hint');`); err != nil {
		return fmt.Errorf("WebView history drag must advertise composer: %w", err)
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('#chat-message').dispatchEvent(new DragEvent('drop',{...window.attachmentPoint,bubbles:true,cancelable:true,dataTransfer:window.attachmentDrag}));`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return document.querySelectorAll('.composer .attachment-card').length === 2 && document.querySelector('.composer img')?.naturalWidth > 0 && !document.querySelector('.composer-drop-hint') && document.querySelector('#chat-message').value === 'Native desktop draft';`); err != nil {
		return err
	}
	point, err := page.EvalContext(ctx, `window.attachmentPoint`)
	if err != nil {
		return err
	}
	coords, ok := point.(map[string]any)
	if !ok {
		return fmt.Errorf("missing composer drop coordinates: %v", point)
	}
	x, y := int(coords["clientX"].(float64)), int(coords["clientY"].(float64))
	nativeDrop := func() error {
		if err := p.rendererSmokeUI(ctx, func() error {
			if p.chatView != view || p.chatPresentedView != view || p.serverDialogOpen || p.tab != "Chat" || p.attachmentDropPending {
				return fmt.Errorf("native drop unavailable: tab=%q presented=%t selected=%t dialog=%t pending=%t", p.tab, p.chatPresentedView == view, p.chatView == view, p.serverDialogOpen, p.attachmentDropPending)
			}
			p.receiveAttachmentDrop(&mygo.FileDropEvent{Paths: paths, X: x, Y: y})
			return nil
		}); err != nil {
			return err
		}
		return p.waitDesktopServerUI(ctx, func() bool { return !p.attachmentDropPending })
	}
	// The DOM event already attached its files. Its native notification must not
	// attach the same batch again. The next native-only drop must still work.
	if err := nativeDrop(); err != nil {
		return err
	}
	if value, err := page.EvalContext(ctx, `document.querySelectorAll('.composer .attachment-card').length`); err != nil || value != float64(2) {
		return fmt.Errorf("DOM/native duplicate guard: count=%v, error=%v", value, err)
	}
	if err := nativeDrop(); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return document.querySelectorAll('.composer .attachment-card').length === 4 && document.querySelector('#chat-message').value === 'Native desktop draft' && Array.from(document.querySelectorAll('.composer img')).every(img => img.naturalWidth > 0);`); err != nil {
		diagnostic, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		state, _ := page.EvalContext(diagnostic, `return JSON.stringify({cards:document.querySelectorAll('.composer .attachment-card').length,draft:document.querySelector('#chat-message')?.value,errors:Array.from(document.querySelectorAll('[role="alert"]')).map(e=>e.textContent),images:Array.from(document.querySelectorAll('.composer img')).map(e=>({width:e.naturalWidth,complete:e.complete}))});`)
		return fmt.Errorf("native file delivery: %w; state=%v", err, state)
	}
	image, err = view.CapturePage()
	if err != nil {
		return err
	}
	if err := os.WriteFile(report+"-composer.png", image, 0o600); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('.composer .image-card .attachment-open').click();`); err != nil {
		return err
	}
	if err := waitChatScript(ctx, page, `return document.querySelector('dialog.image-preview')?.open === true;`); err != nil {
		return err
	}
	if _, err := page.EvalContext(ctx, `document.querySelector('dialog .image-preview-toolbar button').click(); document.querySelector('.composer').requestSubmit();`); err != nil {
		return err
	}
	return waitChatScript(ctx, page, `const sent = window.attachmentSubmission; return sent?.message === 'Native desktop draft' && sent.files.length === 4 && sent.files[0].name === 'notes.txt' && new TextDecoder().decode(new Uint8Array(sent.files[0].bytes)) === 'Native file bytes' && sent.files[1].type === 'image/png' && sent.files[1].bytes.length === 68 && sent.files[2].name === 'native-notes.txt' && new TextDecoder().decode(new Uint8Array(sent.files[2].bytes)) === 'Native callback file bytes' && sent.files[3].name === 'native-image.png' && sent.files[3].type === 'image/png' && sent.files[3].bytes.every((value,index) => value === sent.files[1].bytes[index]);`)
}
