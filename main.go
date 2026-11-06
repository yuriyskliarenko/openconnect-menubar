// OpenConnect Menubar is a macOS menu bar front end for openconnect.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"time"

	"fyne.io/systray"
)

type App struct {
	store  *Store
	runner *Runner
	web    *Web

	mu      sync.Mutex
	busy    string
	lastErr string

	uiMu    sync.Mutex // guards the menu, rebuilt from any goroutine
	status  *systray.MenuItem
	down    *systray.MenuItem
	items   map[string]*systray.MenuItem
	stopped chan struct{} // closed to retire the previous menu's listeners
}

// action runs fn in the background, ignoring requests while one is in flight.
func (a *App) action(label string, fn func() error) {
	a.mu.Lock()
	if a.busy != "" {
		a.mu.Unlock()
		return
	}
	a.busy, a.lastErr = label, ""
	a.mu.Unlock()
	a.refresh()
	go func() {
		err := fn()
		a.mu.Lock()
		a.busy = ""
		if err != nil && !errors.Is(err, errCancelled) {
			a.lastErr = err.Error()
		}
		a.mu.Unlock()
		a.refresh()
	}()
}

func (a *App) refresh() {
	a.uiMu.Lock()
	defer a.uiMu.Unlock()
	if a.status == nil {
		return
	}
	a.mu.Lock()
	busy, lastErr := a.busy, a.lastErr
	a.mu.Unlock()
	active := a.runner.Active()
	name := "Unknown"
	if c, ok := a.store.Get(active); ok {
		name = c.Name
	}
	switch {
	case busy != "":
		systray.SetTitle("🟡 VPN")
		a.status.SetTitle(busy + "…")
	case lastErr != "":
		systray.SetTitle("🔴 VPN")
		a.status.SetTitle("Error: " + lastErr)
	case active != "":
		systray.SetTitle("🟢 " + name)
		a.status.SetTitle("Connected: " + name)
	default:
		systray.SetTitle("⚪️ VPN")
		a.status.SetTitle("Not connected")
	}
	for id, item := range a.items {
		if id == active {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
	if active != "" && busy == "" {
		a.down.Enable()
	} else {
		a.down.Disable()
	}
}

// buildMenu recreates the menu, because connections can change at any time.
func (a *App) buildMenu() {
	a.uiMu.Lock()
	if a.stopped != nil {
		close(a.stopped)
		systray.ResetMenu()
	}
	stop := make(chan struct{})
	a.stopped = stop
	a.status = systray.AddMenuItem("", "")
	a.status.Disable()
	systray.AddSeparator()

	a.items = map[string]*systray.MenuItem{}
	conns := a.store.List()
	if len(conns) == 0 {
		systray.AddMenuItem("No connections yet", "").Disable()
	}
	for _, c := range conns {
		item := systray.AddMenuItemCheckbox(c.Name, "", false)
		a.items[c.ID] = item
		go func() {
			for {
				select {
				case <-stop:
					return
				case <-item.ClickedCh:
					if a.runner.Active() != c.ID {
						a.action("Connecting to "+c.Name, func() error { return a.runner.Connect(c) })
					}
				}
			}
		}()
	}
	systray.AddSeparator()
	down := systray.AddMenuItem("Disconnect", "")
	a.down = down
	manage := systray.AddMenuItem("Manage Connections…", "")
	logs := systray.AddMenuItem("Show Log", "")
	quit := systray.AddMenuItem("Quit (VPN stays up)", "")
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-down.ClickedCh:
				a.action("Disconnecting", a.runner.Disconnect)
			case <-manage.ClickedCh:
				a.openManager()
			case <-logs.ClickedCh:
				exec.Command("/usr/bin/open", "-a", "Console", a.runner.logPath).Start()
			case <-quit.ClickedCh:
				systray.Quit()
			}
		}
	}()
	a.uiMu.Unlock()
	a.refresh()
}

func (a *App) openManager() { exec.Command("/usr/bin/open", a.web.URL()).Start() }

func (a *App) onReady() {
	systray.SetTitle("⚪️ VPN")
	a.buildMenu()
	a.store.onChange = a.buildMenu
	if len(a.store.List()) == 0 {
		a.openManager()
	}
	go func() {
		for range time.Tick(3 * time.Second) {
			a.refresh()
		}
	}()
}

func main() {
	serveOnly := flag.Bool("serve", false, "run only the connection manager and print its URL (for development)")
	flag.Parse()

	dir, err := configDir()
	if err != nil {
		log.Fatal(err)
	}
	store, err := loadStore(dir)
	if err != nil {
		log.Fatal(err)
	}
	a := &App{store: store, runner: newRunner(dir)}
	if a.web, err = startWeb(store, a.runner, a.action); err != nil {
		log.Fatal(err)
	}
	if *serveOnly {
		fmt.Println(a.web.URL())
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		return
	}
	if os.Getenv("OCM_CONFIG_DIR") != "" {
		fmt.Println(a.web.URL()) // development only: lets scripts reach the manager
	}
	systray.Run(a.onReady, nil)
}
