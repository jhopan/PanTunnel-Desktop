package main

import (
	"strconv"
	"strings"
	"time"
)

// hotspotStatus is the report the platform helper returns to the UI.
type hotspotStatus struct {
	Supported bool   // platform can share the tunnel with a hotspot
	Active    bool   // a hotspot / tethering adapter is up
	Adapter   string // hotspot (ICS private) adapter name
	Shared    bool   // the TUN adapter is currently the shared (public) connection
	Clients   int    // neighbours seen on the ICS subnet
	Note      string // extra state or error to show verbatim
}

// hotspotPrefix is the IPv4 prefix Internet Connection Sharing hands out on the
// private side (192.168.137.0/24) — the subnet hotspot clients live on.
const hotspotPrefix = "192.168.137."

// summary renders the state for the status line in the UI.
func (s hotspotStatus) summary() string {
	if !s.Supported {
		return "Hotspot share: tidak didukung di platform ini."
	}
	if !s.Active {
		return "Hotspot share: hotspot belum aktif."
	}
	if !s.Shared {
		return "Hotspot share: hotspot aktif, tunnel belum dibagikan."
	}
	return "Hotspot share: aktif — " + strconv.Itoa(s.Clients) + " klien lewat VPN."
}

// parseHotspotQuery reads the KEY=value report printed by the ICS helper.
// Unknown keys are ignored so the helper can report more without breaking Go.
func parseHotspotQuery(out string) hotspotStatus {
	st := hotspotStatus{Supported: true}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "TUN_PRESENT":
			if value != "1" {
				st.Note = "adapter " + tunInterfaceName + " belum ada"
			}
		case "HOTSPOT_ADAPTER":
			if st.Adapter == "" {
				st.Adapter = value
			}
		case "HOTSPOT_UP":
			st.Active = value == "1"
		case "PUBLIC":
			if value == tunInterfaceName {
				st.Shared = true
			}
		case "CLIENTS":
			if n, err := strconv.Atoi(value); err == nil && n >= 0 {
				st.Clients = n
			}
		case "ERROR":
			st.Note = value
		}
	}
	return st
}

// hotspotWatchID is the interval between hotspot state refreshes. Windows hands
// the shared connection back to the physical adapter whenever the network
// changes, so a stale "shared" flag is not an option.
const hotspotWatchInterval = 15 * time.Second

// hotspotWatch refreshes hotspot state for the UI and re-asserts ICS sharing
// while the tunnel is up and the user asked for hotspot sharing.
func (u *uiState) hotspotWatch() {
	stop := make(chan struct{})
	u.stopJob = stop

	// One query up front so the card can say whether a hotspot adapter exists;
	// after that only a running tunnel or an enabled share costs a query.
	u.storeHotspot(hotspotQuery())

	tick := time.NewTicker(hotspotWatchInterval)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			u.refreshHotspot()
		}
	}
}

// refreshHotspot queries the platform helper and repairs sharing if needed.
func (u *uiState) refreshHotspot() {
	enabled := u.hotspotEnabled()
	if !enabled && !u.connected() {
		return
	}
	if enabled && u.connected() && !u.hotspotSnapshot().Shared {
		u.shareHotspot()
		return
	}
	u.storeHotspot(hotspotQuery())
}

// shareHotspot makes the TUN adapter the connection Windows shares with the
// hotspot, remembering who shared internet before so it can be restored.
func (u *uiState) shareHotspot() {
	previous, err := hotspotEnable(u.restoreHint())
	if previous != "" && previous != u.restoreHint() {
		u.mu.Lock()
		u.data.HotspotRestore = previous
		saveConfig(u.data)
		u.mu.Unlock()
	}
	st := hotspotQuery()
	if err != nil {
		st.Note = err.Error()
	} else {
		st.Note = ""
	}
	u.storeHotspot(st)
}

// unshareHotspot gives internet sharing back to whoever had it.
func (u *uiState) unshareHotspot() {
	if !hotspotPlatformSupported {
		return
	}
	st := u.hotspotSnapshot()
	if !st.Shared && u.restoreHint() == "" {
		return
	}
	hotspotDisable(u.restoreHint())
	u.mu.Lock()
	u.data.HotspotRestore = ""
	saveConfig(u.data)
	u.mu.Unlock()
	u.storeHotspot(hotspotQuery())
}

// toggleHotspot flips the setting and applies it immediately when connected.
func (u *uiState) toggleHotspot() {
	u.mu.Lock()
	u.data.HotspotShare = !u.data.HotspotShare
	enabled := u.data.HotspotShare
	saveConfig(u.data)
	u.mu.Unlock()

	if enabled {
		if u.connected() {
			u.shareHotspot()
		} else {
			u.status = "Hotspot share aktif: menyala saat tunnel tersambung."
			u.storeHotspot(hotspotQuery())
		}
		return
	}
	u.unshareHotspot()
	u.status = "Hotspot share dimatikan."
}

func (u *uiState) hotspotEnabled() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.data.HotspotShare
}

func (u *uiState) restoreHint() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.data.HotspotRestore
}

func (u *uiState) hotspotSnapshot() hotspotStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hotspot
}

func (u *uiState) storeHotspot(st hotspotStatus) {
	u.mu.Lock()
	u.hotspot = st
	u.mu.Unlock()
	u.invalidate()
}

// invalidate asks Gio to redraw; safe to call from any goroutine.
func (u *uiState) invalidate() {
	if u.win != nil {
		u.win.Invalidate()
	}
}
