//go:build darwin

// A native resize diagnostic using synthetic content and disposable app data.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type point struct{ X, Y float64 }

type observation struct {
	Time    int64           `json:"time"`
	Event   string          `json:"event"`
	Mode    string          `json:"mode"`
	Bounds  mygo.Rectangle  `json:"bounds"`
	Live    bool            `json:"live"`
	DelayMS float64         `json:"delay_ms,omitempty"`
	Pointer *point          `json:"pointer,omitempty"`
	Target  *mygo.Rectangle `json:"target,omitempty"`
}

func main() {
	mode := flag.String("content", "embedded", "embedded, direct, or native")
	standard := flag.Bool("standard-titlebar", false, "use the system title bar instead of Desktop's hidden title bar")
	automatic := flag.Bool("automatic", false, "check programmatic horizontal and vertical size changes, then exit")
	pageURL := flag.String("url", "", "load a local synthetic frontend fixture instead of simple HTML")
	cookiePoll := flag.String("cookie-poll", "none", "read WebKit cookies every three seconds: none, main, or worker")
	flag.Parse()
	if *mode != "embedded" && *mode != "direct" && *mode != "native" {
		log.Fatal("invalid --content")
	}
	if *cookiePoll != "none" && *cookiePoll != "main" && *cookiePoll != "worker" {
		log.Fatal("invalid --cookie-poll")
	}
	run := os.Getenv("SPOOL_RUN_ROOT")
	if run == "" {
		log.Fatal("run through sh tools/run.sh desktop-resize bun tools/desktop-resize/run.mjs")
	}

	// Keep filesystem I/O off the UI thread so diagnostics do not cause the stall.
	records := make(chan observation, 4096)
	written := make(chan struct{})
	go func() {
		defer close(written)
		encoder := json.NewEncoder(os.Stdout)
		for record := range records {
			_ = encoder.Encode(record)
		}
	}()
	defer func() { close(records); <-written }()
	emit := func(record observation) {
		record.Time, record.Mode = time.Now().UnixMilli(), *mode
		select {
		case records <- record:
		default:
		}
	}

	mygo.App.SetName("Spool Resize Diagnostic")
	mygo.App.SetPath(mygo.PathUserData, filepath.Join(run, "native"))
	mygo.App.WhenReady(func() {
		done := make(chan struct{})
		var web *mygo.WebView
		opts := mygo.WindowOptions{
			Title: "Spool Resize Diagnostic · " + *mode,
			Width: 1100, Height: 720, MinWidth: 840, MinHeight: 580,
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 16, Y: 20},
		}
		if *pageURL != "" {
			opts.Title += " · Spool UI"
			opts.Width, opts.Height = 1280, 860
			opts.Page = mygo.PageOptions{PreloadScript: fixturePreload}
		}
		if *cookiePoll != "none" {
			opts.Title += " · cookies on " + *cookiePoll
		}
		if *standard {
			opts.TitleBarStyle, opts.TrafficLightPosition = "", nil
		}
		if *mode != "direct" {
			opts.Content = ui.View(func(c *ui.Context) {
				if *mode == "native" {
					ui.Column(c).Fill().Padding(70).Background(ui.RGB(35, 35, 35)).Children(func() {
						ui.Text(c, "Spool Resize Diagnostic — native").TextColor(ui.RGB(240, 240, 240))
						ui.Text(c, "Drag the outer left and right window edges.").TextColor(ui.RGB(220, 220, 220))
					})
				} else if web != nil {
					ui.WebView(c, web).Fill()
				}
			})
		}
		window := mygo.NewWindow(opts)
		var page *mygo.Page
		if *mode != "native" {
			page = window.Page()
			if *mode == "embedded" {
				var err error
				web, err = window.NewWebView(mygo.WebViewOptions{Page: mygo.PageOptions{PreloadScript: fixturePreload}})
				if err != nil {
					log.Fatal(err)
				}
				page = web.Page()
			}
			if *pageURL != "" {
				// The real frontend emits this hint after loading its session.
				// Keep the synthetic document in place, as Desktop does, without
				// starting notification work in this resize-only diagnostic.
				fixture, _ := url.Parse(*pageURL)
				page.OnWillNavigate(func(e *mygo.NavigateEvent) {
					target, err := url.Parse(e.URL)
					if err == nil && fixture != nil && target.Scheme == fixture.Scheme && target.Host == fixture.Host &&
						target.RawPath == "" && target.Fragment == "" && (target.Path == "/__desktop/view" ||
						target.Path == "/__desktop/session-changed" && target.RawQuery == "" && !target.ForceQuery) {
						e.PreventDefault()
					}
				})
				if err := page.LoadURL(*pageURL); err != nil {
					log.Fatal(err)
				}
			} else {
				page.LoadHTML(probeHTML(*mode), "")
			}
			window.Invalidate()
		}

		native := objc.ID(window.NativeHandle())
		liveSelector := objc.RegisterName("inLiveResize")
		mouseSelector := objc.RegisterName("mouseLocation")
		observe := func(event string) observation {
			return observation{Event: event, Bounds: window.Bounds(), Live: objc.Send[bool](native, liveSelector)}
		}
		center := objc.ID(objc.GetClass("NSNotificationCenter")).Send(objc.RegisterName("defaultCenter"))
		var observers []objc.ID
		var blocks []objc.Block
		for _, event := range []string{"WillStartLiveResize", "DidEndLiveResize"} {
			name := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), "NSWindow"+event+"Notification")
			block := objc.NewBlock(func(_ objc.Block, _ objc.ID) { emit(observe(event)) })
			blocks = append(blocks, block)
			observers = append(observers, center.Send(objc.RegisterName("addObserverForName:object:queue:usingBlock:"), name, native, objc.ID(0), block))
		}
		mygo.App.OnQuit(func() {
			close(done)
			emit(observation{Event: "quit"})
			for _, observer := range observers {
				center.Send(objc.RegisterName("removeObserver:"), observer)
			}
			for _, block := range blocks {
				block.Release()
			}
		})
		window.OnResize(func() { emit(observe("resize")) })
		window.OnClose(func(*mygo.CloseEvent) { mygo.App.Quit() })
		window.Show()
		mygo.App.Focus()
		emit(observe("ready"))
		fmt.Fprintf(os.Stderr, "Resize diagnostic ready: pid=%d content=%s gpu=%q\n", os.Getpid(), *mode, os.Getenv("MYGO_GPU"))
		if *cookiePoll != "none" {
			go func() {
				ticker := time.NewTicker(3 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-done:
						return
					case <-ticker.C:
					}
					poll := func() {
						_, err := mygo.Cookies.List(mygo.CookieFilter{URL: "https://resize-fixture.invalid/", Name: "spool_session", Path: "/"})
						if err != nil {
							log.Printf("cookie poll: %v", err)
						}
					}
					if *cookiePoll == "main" {
						window.Update(func() {
							emit(observe("cookie-start"))
							poll()
							emit(observe("cookie-end"))
						})
					} else {
						mygo.RunOnMain(func() { emit(observe("cookie-start")) })
						poll()
						mygo.RunOnMain(func() { emit(observe("cookie-end")) })
					}
				}
			}()
		}

		if *automatic {
			go func() {
				if *pageURL != "" {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					for {
						result, err := page.EvalContext(ctx, `return document.querySelectorAll('.turn').length === 60`)
						if err == nil && result == true {
							break
						}
						if ctx.Err() != nil {
							log.Printf("frontend fixture did not render: %v", ctx.Err())
							mygo.App.Quit()
							return
						}
						select {
						case <-time.After(100 * time.Millisecond):
						case <-done:
							return
						}
					}
				}
				select {
				case <-time.After(time.Second):
				case <-done:
					return
				}
				for i := 0; i < 120; i++ {
					start := time.Now()
					mygo.RunOnMain(func() {
						width, height := 1100, 720
						step := i % 60
						if step >= 30 {
							step = 59 - step
						}
						if i < 60 {
							width += step * 6
						} else {
							height += step * 4
						}
						window.SetSize(width, height)
						record := observe("automatic")
						record.Target = &mygo.Rectangle{Width: width, Height: height}
						record.DelayMS = float64(time.Since(start).Microseconds()) / 1000
						emit(record)
					})
					select {
					case <-time.After(16 * time.Millisecond):
					case <-done:
						return
					}
				}
				mygo.App.Quit()
			}()
		} else {
			go func() {
				for i := 0; i < 36000; i++ {
					select {
					case <-time.After(50 * time.Millisecond):
					case <-done:
						return
					}
					start := time.Now()
					mygo.RunOnMain(func() {
						record := observe("tick")
						record.DelayMS = float64(time.Since(start).Microseconds()) / 1000
						if record.Live {
							p := objc.Send[point](objc.ID(objc.GetClass("NSEvent")), mouseSelector)
							record.Pointer = &p
						}
						if record.Live || record.DelayMS > 100 || i%20 == 0 {
							emit(record)
						}
					})
				}
				mygo.App.Quit()
			}()
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

const fixturePreload = `Object.defineProperty(window, "spoolDesktop", {value: true});
window.WebSocket = class extends EventTarget { readyState = 0; send() {} close() { this.readyState = 3; } };
document.addEventListener("DOMContentLoaded", () => {
  document.documentElement.dataset.desktopPlatform = "darwin";
  document.documentElement.style.setProperty("--desktop-controls-left", "80px");
  localStorage.setItem("spool.ui-preferences.v1", JSON.stringify({language: "zh-CN", theme: "light"}));
}, {once: true});`

func probeHTML(mode string) string {
	var body strings.Builder
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&body, "<p>合成消息 %d：横向缩放时文字会重新换行。This is synthetic text used to exercise window resizing, layout and scrolling. <code>window bounds / WebView layout</code></p>", i+1)
	}
	return `<!doctype html><meta charset="utf-8"><style>
*{box-sizing:border-box}html{color-scheme:dark}body{margin:0;font:14px/1.6 system-ui;display:grid;grid-template-columns:240px minmax(0,1fr) 260px;height:100vh;color:#ddd;background:#242423}
aside{padding:70px 20px;background:#202020;overflow:hidden}main{display:flex;flex-direction:column;min-width:0;min-height:0}header{padding:14px 20px;height:54px;border-bottom:1px solid #444;--app-region:drag}.history{flex:1;overflow:auto;padding:16px 26px}h1{font-size:20px}footer{padding:16px;border-top:1px solid #444}textarea{width:100%;height:70px;background:#303030;color:#eee;border:1px solid #555;border-radius:12px;padding:12px}code{font-size:12px}
</style><aside>Spool Resize Diagnostic<br><br>独立诊断窗口<br>仅使用合成内容<br><br>关闭窗口即可结束</aside><main><header>窗口缩放诊断 · ` + mode + `</header><div class="history"><h1>请拖动整个窗口外侧的左右边框</h1><p>试着向外放大、向内缩小，观察是否会中途停住。日志仅包含尺寸、时间和缩放期间的鼠标位置。</p>` + body.String() + `</div><footer><textarea placeholder="Synthetic composer"></textarea></footer></main><aside>原生窗口配置与 Desktop 一致<br><br>日志会记录缩放开始、结束与主线程延迟。</aside>`
}
