package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

const (
	appName = "PanTunnel"
	// appVersion di-bump saat rilis; lihat juga setup.iss.
	appVersion     = "1.0.1"
	defaultPingURL = "http://connectivitycheck.gstatic.com/generate_204"
	// 3 detik = handshake TLS baru tiap 3 detik lewat tunnel. 15 detik sudah
	// cukup untuk tampilan ping dan jauh lebih hemat CPU/koneksi.
	defaultPingInterval = 15
	minPingInterval     = 5
)

//go:embed assets/app_icon.png
var keyIconPNG []byte

var keyImage = loadKeyImage()

func loadKeyImage() image.Image {
	img, err := png.Decode(bytes.NewReader(keyIconPNG))
	if err != nil {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	return img
}

type profile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Port    string `json:"port"`
	UUID    string `json:"uuid"`
	Path    string `json:"path"`
	SNI     string `json:"sni"`
	Host    string `json:"host"`
	// LicenseExpire diisi hanya kalau profil datang dari file lisensi .pantunnel.
	LicenseExpire string `json:"license_expire,omitempty"`
}

type configData struct {
	Profiles       []profile `json:"profiles"`
	ActiveID       string    `json:"active_id"`
	HTTPPing       bool      `json:"http_ping"`
	PingInterval   int       `json:"ping_interval"`
	PingURL        string    `json:"ping_url"`
	InstallationID string    `json:"installation_id"`
}

type currentScreen int

const (
	screenMain currentScreen = iota
	screenProfiles
	screenEdit
	screenSettings
)

type uiState struct {
	win    *app.Window
	data   configData
	screen currentScreen
	editID string
	status string
	pingMs string

	cmd      *exec.Cmd
	mu       sync.Mutex
	stopPing chan struct{}

	btnConnect       widget.Clickable
	btnNavClipboard  widget.Clickable
	btnNavFile       widget.Clickable
	btnNavSettings   widget.Clickable
	btnBack          widget.Clickable
	btnAddProfile    widget.Clickable
	btnSaveProfile   widget.Clickable
	btnCancelProfile widget.Clickable
	btnSaveSettings  widget.Clickable
	btnCopyHWID      widget.Clickable

	selectButtons []widget.Clickable
	editButtons   []widget.Clickable
	deleteButtons []widget.Clickable
	profileList   widget.List

	cbPingEnabled widget.Bool

	edName     widget.Editor
	edAddress  widget.Editor
	edPort     widget.Editor
	edUUID     widget.Editor
	edPath     widget.Editor
	edSNI      widget.Editor
	edHost     widget.Editor
	edInterval widget.Editor
	edPingURL  widget.Editor
}

func main() {
	if code, handled := runCLI(os.Args[1:]); handled {
		os.Exit(code)
	}
	// Satu instance: yang kedua tidak boleh membuat TUN kedua. Cukup bawa
	// window yang sudah jalan ke depan lalu keluar.
	if anotherInstance() {
		focusExistingWindow()
		os.Exit(0)
	}
	// UAC prompt for TUN. On Linux/macOS this is a no-op.
	// ensureElevated melepas mutex sebelum relaunch supaya instance elevated
	// bisa mengambilnya.
	if !ensureElevated() {
		os.Exit(0)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(32 << 20)
	}
	// One-time privilege setup for Linux/macOS:
	// sets cap_net_admin (Linux) or setuid (macOS) on core binary so TUN
	// works without root on subsequent launches. Shows native GUI password
	// dialog (pkexec/osascript). No-op on Windows.
	corePath := filepath.Join(filepath.Dir(os.Args[0]), coreNames[0])
	_ = runOneTimeSetup(corePath)
	debug.SetGCPercent(50)
	go run()
	app.Main()
}

func run() {
	w := new(app.Window)
	w.Option(
		app.Title(appName),
		app.Size(unit.Dp(420), unit.Dp(640)),
		app.MinSize(unit.Dp(420), unit.Dp(640)),
	)
	var ops op.Ops
	u := initUI(loadConfig())
	u.win = w
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			u.stop()
			return
		case app.FrameEvent:
			setWindowDarkMode()
			gtx := app.NewContext(&ops, e)
			u.draw(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

// normalizePingInterval menjaga interval probe di rentang yang masuk akal:
// terlalu kecil berarti handshake TCP+TLS+WS baru lewat tunnel tiap beberapa detik.
func normalizePingInterval(seconds int) int {
	if seconds < minPingInterval {
		return defaultPingInterval
	}
	return seconds
}

func initUI(cfg configData) *uiState {
	if cfg.InstallationID == "" {
		cfg.InstallationID = newID()
	}
	cfg.PingInterval = normalizePingInterval(cfg.PingInterval)
	if cfg.PingURL == "" {
		cfg.PingURL = defaultPingURL
	}
	if len(cfg.Profiles) == 0 {
		cfg.Profiles = []profile{{
			ID:      newID(),
			Name:    "Default",
			Address: "",
			Port:    "443",
			UUID:    "",
			Path:    "/vless",
			SNI:     "",
			Host:    "",
		}}
		cfg.ActiveID = cfg.Profiles[0].ID
	}
	if cfg.ActiveID == "" {
		cfg.ActiveID = cfg.Profiles[0].ID
	}

	u := &uiState{
		data:   cfg,
		screen: screenMain,
		status: "Disconnected",
		pingMs: "-",
	}
	u.profileList.Axis = layout.Vertical

	for _, ed := range []*widget.Editor{
		&u.edName, &u.edAddress, &u.edPort, &u.edUUID, &u.edPath,
		&u.edSNI, &u.edHost, &u.edInterval, &u.edPingURL,
	} {
		ed.SingleLine = true
	}
	saveConfig(cfg)
	return u
}

func uiTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th.Palette = material.Palette{
		Bg:         color.NRGBA{R: 14, G: 17, B: 23, A: 255},
		Fg:         color.NRGBA{R: 240, G: 246, B: 252, A: 255},
		ContrastBg: color.NRGBA{R: 16, G: 185, B: 129, A: 255},
		ContrastFg: color.NRGBA{R: 255, G: 255, B: 255, A: 255},
	}
	return th
}

func card(gtx layout.Context, bg, border color.NRGBA, r unit.Dp, w layout.Widget) layout.Dimensions {
	return widget.Border{
		Color:        border,
		CornerRadius: r,
		Width:        unit.Dp(1),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Stack{}.Layout(gtx,
			layout.Expanded(func(gtx layout.Context) layout.Dimensions {
				rr := gtx.Dp(r)
				rect := image.Rectangle{Max: gtx.Constraints.Min}
				paint.FillShape(gtx.Ops, bg, clip.UniformRRect(rect, rr).Op(gtx.Ops))
				return layout.Dimensions{Size: gtx.Constraints.Min}
			}),
			layout.Stacked(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, w)
			}),
		)
	})
}

func divider(gtx layout.Context, c color.NRGBA) layout.Dimensions {
	d := image.Pt(gtx.Constraints.Max.X, gtx.Dp(unit.Dp(1)))
	paint.FillShape(gtx.Ops, c, clip.Rect{Max: d}.Op())
	return layout.Dimensions{Size: d}
}

func (u *uiState) draw(gtx layout.Context) layout.Dimensions {
	th := uiTheme()
	paint.Fill(gtx.Ops, th.Palette.Bg)

	return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		switch u.screen {
		case screenEdit:
			return u.drawEditScreen(gtx, th)
		case screenSettings:
			return u.drawSettingsScreen(gtx, th)
		default:
			return u.drawMainScreen(gtx, th)
		}
	})
}

func (u *uiState) drawMainScreen(gtx layout.Context, th *material.Theme) layout.Dimensions {
	u.ensureButtons()
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return u.drawHeader(gtx, th)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, unit.Sp(14), "Daftar Profil")
					l.Font.Weight = font.Bold
					l.Color = color.NRGBA{R: 240, G: 246, B: 252, A: 255}
					return l.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			var toSelectID, toEditID, toDeleteID string

			dims := material.List(th, &u.profileList).Layout(gtx, len(u.data.Profiles), func(gtx layout.Context, i int) layout.Dimensions {
				if i >= len(u.data.Profiles) || i >= len(u.selectButtons) {
					return layout.Dimensions{}
				}
				p := u.data.Profiles[i]
				for u.selectButtons[i].Clicked(gtx) {
					toSelectID = p.ID
				}
				for u.editButtons[i].Clicked(gtx) {
					toEditID = p.ID
				}
				for u.deleteButtons[i].Clicked(gtx) {
					toDeleteID = p.ID
				}

				isActive := p.ID == u.data.ActiveID
				name := p.Name
				if isActive {
					name = "● " + name
				}
				endpoint := "Belum dikonfigurasi"
				if p.Address != "" {
					endpoint = p.Address + ":" + p.Port
					if p.Path != "" {
						endpoint += " • " + p.Path
					}
				}

				return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					cardBg := color.NRGBA{R: 22, G: 27, B: 34, A: 255}
					borderCol := color.NRGBA{R: 48, G: 54, B: 61, A: 255}
					if isActive {
						borderCol = color.NRGBA{R: 16, G: 185, B: 129, A: 255}
					}
					return card(gtx, cardBg, borderCol, unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										l := material.Label(th, unit.Sp(14), name)
										l.Font.Weight = font.Bold
										if isActive {
											l.Color = color.NRGBA{R: 16, G: 185, B: 129, A: 255}
										} else {
											l.Color = color.NRGBA{R: 240, G: 246, B: 252, A: 255}
										}
										return l.Layout(gtx)
									}),
									layout.Rigid(layout.Spacer{Height: unit.Dp(2)}.Layout),
									layout.Rigid(func(gtx layout.Context) layout.Dimensions {
										l := material.Label(th, unit.Sp(12), endpoint)
										l.Color = color.NRGBA{R: 139, G: 148, B: 158, A: 255}
										return l.Layout(gtx)
									}),
								)
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								btnLabel := "Pilih"
								if isActive {
									btnLabel = "Aktif"
								}
								b := material.Button(th, &u.selectButtons[i], btnLabel)
								if isActive {
									b.Background = color.NRGBA{R: 16, G: 185, B: 129, A: 255}
									b.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
								} else {
									b.Background = color.NRGBA{R: 33, G: 38, B: 45, A: 255}
									b.Color = color.NRGBA{R: 201, G: 209, B: 217, A: 255}
								}
								b.TextSize = unit.Sp(11)
								b.CornerRadius = unit.Dp(4)
								return b.Layout(gtx)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								b := material.Button(th, &u.editButtons[i], "Edit")
								b.Background = color.NRGBA{R: 33, G: 38, B: 45, A: 255}
								b.Color = color.NRGBA{R: 201, G: 209, B: 217, A: 255}
								b.TextSize = unit.Sp(11)
								b.CornerRadius = unit.Dp(4)
								return b.Layout(gtx)
							}),
							layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								b := material.Button(th, &u.deleteButtons[i], "Hapus")
								b.Background = color.NRGBA{R: 45, G: 25, B: 25, A: 255}
								b.Color = color.NRGBA{R: 248, G: 113, B: 113, A: 255}
								b.TextSize = unit.Sp(11)
								b.CornerRadius = unit.Dp(4)
								return b.Layout(gtx)
							}),
						)
					})
				})
			})

			if toSelectID != "" {
				u.data.ActiveID = toSelectID
				saveConfig(u.data)
				if p := u.getActiveProfile(); p != nil {
					u.status = "Profil aktif: " + p.Name
				}
				if u.win != nil {
					u.win.Invalidate()
				}
			}
			if toEditID != "" {
				u.openEditView(toEditID)
				if u.win != nil {
					u.win.Invalidate()
				}
			}
			if toDeleteID != "" {
				u.deleteProfile(toDeleteID)
				if u.win != nil {
					u.win.Invalidate()
				}
			}

			return dims
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return divider(gtx, color.NRGBA{R: 33, G: 38, B: 45, A: 255})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return u.drawActionButtons(gtx, th)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(12)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return u.drawStatusCard(gtx, th)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, unit.Sp(11), "v"+appVersion+" • By JhopanStore")
				l.Color = color.NRGBA{R: 110, G: 118, B: 129, A: 255}
				return l.Layout(gtx)
			})
		}),
	)
}

func (u *uiState) drawHeader(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(unit.Dp(26)), gtx.Dp(unit.Dp(26))))
					return widget.Image{Src: paint.NewImageOp(keyImage), Fit: widget.Contain}.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, unit.Sp(16), appName)
					l.Font.Weight = font.Bold
					l.Color = color.NRGBA{R: 240, G: 246, B: 252, A: 255}
					return l.Layout(gtx)
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					for u.btnNavClipboard.Clicked(gtx) {
						u.importFromClipboard()
					}
					b := material.Button(th, &u.btnNavClipboard, "Clipboard")
					b.Background = color.NRGBA{R: 22, G: 27, B: 34, A: 255}
					b.Color = color.NRGBA{R: 201, G: 209, B: 217, A: 255}
					b.CornerRadius = unit.Dp(6)
					b.TextSize = unit.Sp(12)
					return b.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					for u.btnNavFile.Clicked(gtx) {
						u.importFromFile()
					}
					b := material.Button(th, &u.btnNavFile, "File")
					b.Background = color.NRGBA{R: 22, G: 27, B: 34, A: 255}
					b.Color = color.NRGBA{R: 201, G: 209, B: 217, A: 255}
					b.CornerRadius = unit.Dp(6)
					b.TextSize = unit.Sp(12)
					return b.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					for u.btnNavSettings.Clicked(gtx) {
						u.openSettingsView()
					}
					b := material.Button(th, &u.btnNavSettings, "Pengaturan")
					b.Background = color.NRGBA{R: 22, G: 27, B: 34, A: 255}
					b.Color = color.NRGBA{R: 201, G: 209, B: 217, A: 255}
					b.CornerRadius = unit.Dp(6)
					b.TextSize = unit.Sp(12)
					return b.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return divider(gtx, color.NRGBA{R: 33, G: 38, B: 45, A: 255})
		}),
	)
}

func (u *uiState) drawActionButtons(gtx layout.Context, th *material.Theme) layout.Dimensions {
	isConnected := u.connected()
	for u.btnConnect.Clicked(gtx) {
		if isConnected {
			u.stop()
		} else {
			u.start()
		}
	}

	label := "CONNECT"
	btnBg := color.NRGBA{R: 16, G: 185, B: 129, A: 255} // Emerald Green
	if isConnected {
		label = "DISCONNECT"
		btnBg = color.NRGBA{R: 239, G: 68, B: 68, A: 255} // Red
	} else if u.status == "Connecting..." {
		label = "CONNECTING..."
		btnBg = color.NRGBA{R: 245, G: 158, B: 11, A: 255} // Amber
	}

	b := material.Button(th, &u.btnConnect, label)
	b.Background = btnBg
	b.Color = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	b.CornerRadius = unit.Dp(8)
	b.TextSize = unit.Sp(15)
	return b.Layout(gtx)
}

func (u *uiState) drawStatusCard(gtx layout.Context, th *material.Theme) layout.Dimensions {
	isConnected := u.connected()
	return card(gtx, color.NRGBA{R: 22, G: 27, B: 34, A: 255}, color.NRGBA{R: 48, G: 54, B: 61, A: 255}, unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(th, unit.Sp(14), u.status)
				l.Font.Weight = font.Bold
				if isConnected {
					l.Color = color.NRGBA{R: 16, G: 185, B: 129, A: 255}
				} else if strings.HasPrefix(u.status, "GAGAL:") {
					l.Color = color.NRGBA{R: 239, G: 68, B: 68, A: 255}
				} else {
					l.Color = color.NRGBA{R: 139, G: 148, B: 158, A: 255}
				}
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				pingText := "HTTP Ping: " + u.pingMs
				if !u.data.HTTPPing {
					pingText = "HTTP Ping: Dinonaktifkan"
				}
				l := material.Label(th, unit.Sp(12), pingText)
				if isConnected && strings.Contains(u.pingMs, "ms") {
					l.Color = color.NRGBA{R: 52, G: 211, B: 153, A: 255} // Light Green
				} else {
					l.Color = color.NRGBA{R: 139, G: 148, B: 158, A: 255}
				}
				return l.Layout(gtx)
			}),
		)
	})
}

func (u *uiState) drawEditScreen(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return u.drawSubHeader(gtx, th, "Edit Profil VLESS")
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(labeledField(gtx, th, "Nama Profil", "", inputField(th, &u.edName, "contoh: Server Utama")).Layout),
		layout.Rigid(labeledField(gtx, th, "Target Server", "", inputField(th, &u.edAddress, "contoh: ava.game.naver.com")).Layout),
		layout.Rigid(labeledField(gtx, th, "Port", "", inputField(th, &u.edPort, "443")).Layout),
		layout.Rigid(labeledField(gtx, th, "Account UUID", "", inputField(th, &u.edUUID, "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx")).Layout),
		layout.Rigid(labeledField(gtx, th, "WebSocket Path", "", inputField(th, &u.edPath, "/vless")).Layout),
		layout.Rigid(labeledField(gtx, th, "SNI (Server Name)", "", inputField(th, &u.edSNI, "contoh: support.zoom.us")).Layout),
		layout.Rigid(labeledField(gtx, th, "Host Header", "", inputField(th, &u.edHost, "sama dengan SNI")).Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			for u.btnSaveProfile.Clicked(gtx) {
				u.saveProfileForm()
			}
			for u.btnCancelProfile.Clicked(gtx) {
				u.screen = screenMain
				u.status = "Batal edit profil."
			}
			return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					b := material.Button(th, &u.btnSaveProfile, "SIMPAN")
					b.Background = color.NRGBA{R: 16, G: 185, B: 129, A: 255}
					b.CornerRadius = unit.Dp(6)
					return b.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(10)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					b := material.Button(th, &u.btnCancelProfile, "BATAL")
					b.Background = color.NRGBA{R: 33, G: 38, B: 45, A: 255}
					b.CornerRadius = unit.Dp(6)
					return b.Layout(gtx)
				}),
			)
		}),
	)
}

func (u *uiState) drawSettingsScreen(gtx layout.Context, th *material.Theme) layout.Dimensions {
	hwidStr := makeHWID(u.data.InstallationID)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return u.drawSubHeader(gtx, th, "Pengaturan")
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return card(gtx, color.NRGBA{R: 22, G: 27, B: 34, A: 255}, color.NRGBA{R: 48, G: 54, B: 61, A: 255}, unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, unit.Sp(11), "Device HWID")
						l.Color = color.NRGBA{R: 139, G: 148, B: 158, A: 255}
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(4)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Label(th, unit.Sp(13), hwidStr)
						l.Color = color.NRGBA{R: 0, G: 229, B: 255, A: 255} // Cyan
						return l.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						for u.btnCopyHWID.Clicked(gtx) {
							u.copyHWID()
						}
						b := material.Button(th, &u.btnCopyHWID, "SALIN HWID")
						b.Background = color.NRGBA{R: 22, G: 27, B: 34, A: 255}
						b.Color = color.NRGBA{R: 201, G: 209, B: 217, A: 255}
						b.TextSize = unit.Sp(11)
						b.CornerRadius = unit.Dp(4)
						return b.Layout(gtx)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						label := "Kirim HWID ini ke admin untuk mendapat file lisensi .pantunnel."
						if p := u.getActiveProfile(); p != nil && p.LicenseExpire != "" {
							label = "Profil aktif: " + licenseSummary(p.LicenseExpire) + "."
						}
						l := material.Label(th, unit.Sp(10), label)
						l.Color = color.NRGBA{R: 110, G: 118, B: 129, A: 255}
						return l.Layout(gtx)
					}),
				)
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			cb := material.CheckBox(th, &u.cbPingEnabled, "HTTP Ping Keep-Alive")
			cb.Color = color.NRGBA{R: 16, G: 185, B: 129, A: 255}
			return cb.Layout(gtx)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
		layout.Rigid(inputField(th, &u.edInterval, "Interval Detik (default: 15)").Layout),
		layout.Rigid(inputField(th, &u.edPingURL, "URL Ping (204 Endpoint)").Layout),
		layout.Rigid(layout.Spacer{Height: unit.Dp(14)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			for u.btnSaveSettings.Clicked(gtx) {
				u.saveSettingsForm()
			}
			b := material.Button(th, &u.btnSaveSettings, "SIMPAN PENGATURAN")
			b.Background = color.NRGBA{R: 16, G: 185, B: 129, A: 255}
			b.CornerRadius = unit.Dp(6)
			return b.Layout(gtx)
		}),
	)
}

func (u *uiState) drawSubHeader(gtx layout.Context, th *material.Theme, title string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					for u.btnBack.Clicked(gtx) {
						u.screen = screenMain
					}
					b := material.Button(th, &u.btnBack, "← Kembali")
					b.Background = color.NRGBA{R: 22, G: 27, B: 34, A: 255}
					b.Color = color.NRGBA{R: 201, G: 209, B: 217, A: 255}
					b.TextSize = unit.Sp(12)
					b.CornerRadius = unit.Dp(4)
					return b.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					l := material.Label(th, unit.Sp(16), title)
					l.Font.Weight = font.Bold
					l.Color = color.NRGBA{R: 240, G: 246, B: 252, A: 255}
					return l.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(10)}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return divider(gtx, color.NRGBA{R: 33, G: 38, B: 45, A: 255})
		}),
	)
}

type inputWrapper struct {
	th    *material.Theme
	ed    *widget.Editor
	label string
}

func inputField(th *material.Theme, ed *widget.Editor, label string) inputWrapper {
	return inputWrapper{th: th, ed: ed, label: label}
}

func (iw inputWrapper) Layout(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Bottom: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return card(gtx, color.NRGBA{R: 22, G: 27, B: 34, A: 255}, color.NRGBA{R: 48, G: 54, B: 61, A: 255}, unit.Dp(6), func(gtx layout.Context) layout.Dimensions {
			ed := material.Editor(iw.th, iw.ed, iw.label)
			ed.TextSize = unit.Sp(13)
			ed.Color = color.NRGBA{R: 240, G: 246, B: 252, A: 255}
			ed.HintColor = color.NRGBA{R: 110, G: 118, B: 129, A: 255}
			return ed.Layout(gtx)
		})
	})
}

// labeledField wraps an input field with a bold label on top and a light hint below.
type labeledWrapper struct {
	th    *material.Theme
	name  string
	hint  string
	inner inputWrapper
}

func labeledField(_ layout.Context, th *material.Theme, name, hint string, inner inputWrapper) labeledWrapper {
	return labeledWrapper{th: th, name: name, hint: hint, inner: inner}
}

func (lw labeledWrapper) Layout(gtx layout.Context) layout.Dimensions {
	return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				l := material.Label(lw.th, unit.Sp(12), lw.name)
				l.Font.Weight = font.Bold
				l.Color = color.NRGBA{R: 201, G: 209, B: 217, A: 255}
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Height: unit.Dp(3)}.Layout),
			layout.Rigid(lw.inner.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if lw.hint == "" {
					return layout.Dimensions{}
				}
				l := material.Label(lw.th, unit.Sp(11), lw.hint)
				l.Color = color.NRGBA{R: 110, G: 118, B: 129, A: 255}
				return layout.Inset{Bottom: unit.Dp(4)}.Layout(gtx, l.Layout)
			}),
		)
	})
}

func (u *uiState) ensureButtons() {
	n := len(u.data.Profiles)
	if len(u.selectButtons) != n {
		u.selectButtons = make([]widget.Clickable, n)
		u.editButtons = make([]widget.Clickable, n)
		u.deleteButtons = make([]widget.Clickable, n)
	}
}

func (u *uiState) getActiveProfile() *profile {
	for i := range u.data.Profiles {
		if u.data.Profiles[i].ID == u.data.ActiveID {
			return &u.data.Profiles[i]
		}
	}
	if len(u.data.Profiles) > 0 {
		return &u.data.Profiles[0]
	}
	return nil
}

func (u *uiState) openEditView(id string) {
	u.editID = id
	var p *profile
	if id != "" {
		for i := range u.data.Profiles {
			if u.data.Profiles[i].ID == id {
				p = &u.data.Profiles[i]
				break
			}
		}
	}
	if p == nil {
		p = &profile{Port: "443", Path: "/vless"}
	}
	u.edName.SetText(p.Name)
	u.edAddress.SetText(p.Address)
	u.edPort.SetText(p.Port)
	u.edUUID.SetText(p.UUID)
	u.edPath.SetText(p.Path)
	u.edSNI.SetText(p.SNI)
	u.edHost.SetText(p.Host)
	u.screen = screenEdit
}

func (u *uiState) saveProfileForm() {
	p := profile{
		ID:      u.editID,
		Name:    strings.TrimSpace(u.edName.Text()),
		Address: strings.TrimSpace(u.edAddress.Text()),
		Port:    strings.TrimSpace(u.edPort.Text()),
		UUID:    strings.TrimSpace(u.edUUID.Text()),
		Path:    strings.TrimSpace(u.edPath.Text()),
		SNI:     strings.TrimSpace(u.edSNI.Text()),
		Host:    strings.TrimSpace(u.edHost.Text()),
	}
	if p.Name == "" {
		p.Name = "Profil"
	}
	if p.Port == "" {
		p.Port = "443"
	}
	if p.Path == "" {
		p.Path = "/vless"
	}
	if p.Address == "" || p.UUID == "" || p.SNI == "" || p.Host == "" {
		u.status = "GAGAL: field VLESS belum lengkap."
		return
	}
	if _, err := parsePort(p.Port); err != nil {
		u.status = "GAGAL: port harus 1 sampai 65535."
		return
	}
	if p.ID == "" {
		p.ID = newID()
		u.data.Profiles = append(u.data.Profiles, p)
		u.data.ActiveID = p.ID
	} else {
		for i := range u.data.Profiles {
			if u.data.Profiles[i].ID == p.ID {
				u.data.Profiles[i] = p
				break
			}
		}
	}
	u.ensureButtons()
	saveConfig(u.data)
	u.screen = screenMain
	u.status = "Profil disimpan: " + p.Name
}

func (u *uiState) deleteProfile(id string) {
	if u.connected() {
		u.status = "GAGAL: putuskan tunnel sebelum hapus profil."
		return
	}
	if len(u.data.Profiles) <= 1 {
		u.status = "GAGAL: minimal satu profil harus ada."
		return
	}
	idx := -1
	for i, p := range u.data.Profiles {
		if p.ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		u.data.Profiles = append(u.data.Profiles[:idx], u.data.Profiles[idx+1:]...)
	}
	if u.data.ActiveID == id && len(u.data.Profiles) > 0 {
		u.data.ActiveID = u.data.Profiles[0].ID
	}
	u.ensureButtons()
	saveConfig(u.data)
	u.status = "Profil berhasil dihapus."
}

func (u *uiState) importFromClipboard() {
	clip := readClipboard()
	if strings.TrimSpace(clip) == "" {
		u.status = "GAGAL: clipboard kosong."
		return
	}
	u.importText("clipboard", clip)
}

func (u *uiState) importFromFile() {
	filePath, err := chooseImportFile()
	if err != nil || filePath == "" {
		return
	}
	raw, err := os.ReadFile(filePath)
	if err != nil {
		u.status = "GAGAL: file tidak bisa dibaca."
		return
	}
	u.importText(filepath.Base(filePath), string(raw))
}

// importText menambahkan profil dari teks (clipboard atau isi file) yang memuat
// satu atau banyak URI vless://. Satu URI → form edit untuk dikonfirmasi;
// banyak URI → langsung masuk daftar profil.
func (u *uiState) importText(source, text string) {
	if isLicenseFile([]byte(text)) {
		u.importLicense(text)
		return
	}
	list := parseURIList(text)
	if len(list) == 0 {
		u.status = "GAGAL: tidak ada URI vless:// WS + TLS di " + source + "."
		return
	}
	if len(list) == 1 {
		u.prefillEdit(list[0])
		return
	}

	added := 0
	for _, p := range list {
		if u.hasProfile(p) {
			continue
		}
		p.ID = newID()
		if p.Name == "" {
			p.Name = p.Address
		}
		u.data.Profiles = append(u.data.Profiles, p)
		added++
	}
	if added == 0 {
		u.status = "Tidak ada profil baru di " + source + " (semua sudah ada)."
		return
	}
	if u.data.ActiveID == "" {
		u.data.ActiveID = u.data.Profiles[0].ID
	}
	u.ensureButtons()
	saveConfig(u.data)
	u.screen = screenMain
	u.status = fmt.Sprintf("%d profil diimpor dari %s · %d duplikat dilewati.", added, source, len(list)-added)
	u.invalidate()
}

// importLicense membuka file lisensi .pantunnel memakai HWID perangkat ini.
func (u *uiState) importLicense(text string) {
	p, err := openLicense([]byte(text), makeHWID(u.data.InstallationID), time.Now())
	if err != nil {
		u.status = "GAGAL: " + err.Error()
		return
	}
	if u.hasProfile(p) {
		u.status = "Profil dari lisensi ini sudah ada di daftar."
		return
	}
	p.ID = newID()
	u.data.Profiles = append(u.data.Profiles, p)
	if u.data.ActiveID == "" {
		u.data.ActiveID = p.ID
	}
	u.ensureButtons()
	saveConfig(u.data)
	u.screen = screenMain
	u.status = fmt.Sprintf("Lisensi OK: profil %s ditambahkan (%s).", p.Name, licenseSummary(p.LicenseExpire))
	u.invalidate()
}

// copyHWID menaruh HWID di clipboard supaya bisa dikirim ke admin.
func (u *uiState) copyHWID() {
	hwid := makeHWID(u.data.InstallationID)
	if err := writeClipboard(hwid); err != nil {
		u.status = "GAGAL: HWID tidak bisa disalin — " + err.Error()
		return
	}
	u.status = "HWID disalin ke clipboard: " + hwid
	u.invalidate()
}

func (u *uiState) hasProfile(p profile) bool {
	key := profileKey(p)
	for _, existing := range u.data.Profiles {
		if profileKey(existing) == key {
			return true
		}
	}
	return false
}

// prefillEdit membuka form edit dengan isi URI yang baru diimpor; user masih
// bisa mengubah nama/SNI sebelum menyimpan.
func (u *uiState) prefillEdit(p profile) {
	u.editID = ""
	u.edName.SetText(p.Name)
	u.edAddress.SetText(p.Address)
	u.edPort.SetText(p.Port)
	u.edUUID.SetText(p.UUID)
	u.edPath.SetText(p.Path)
	u.edSNI.SetText(p.SNI)
	u.edHost.SetText(p.Host)
	u.screen = screenEdit
	u.status = "URI valid. Konfirmasi lalu SIMPAN."
}

func (u *uiState) openSettingsView() {
	u.cbPingEnabled.Value = u.data.HTTPPing
	u.edInterval.SetText(strconv.Itoa(u.data.PingInterval))
	u.edPingURL.SetText(u.data.PingURL)
	u.screen = screenSettings
}

func (u *uiState) saveSettingsForm() {
	n, err := strconv.Atoi(strings.TrimSpace(u.edInterval.Text()))
	if err != nil || n < minPingInterval {
		u.status = fmt.Sprintf("GAGAL: interval minimal %d detik.", minPingInterval)
		return
	}
	target := strings.TrimSpace(u.edPingURL.Text())
	if target == "" {
		target = defaultPingURL
	}
	if _, err := url.ParseRequestURI(target); err != nil {
		u.status = "GAGAL: URL ping tidak valid."
		return
	}
	u.data.HTTPPing = u.cbPingEnabled.Value
	u.data.PingInterval = n
	u.data.PingURL = target
	saveConfig(u.data)
	u.screen = screenMain
	u.status = "Pengaturan disimpan."
}

func (u *uiState) connected() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.cmd != nil && u.cmd.Process != nil
}

// invalidate asks Gio to redraw; safe to call from any goroutine.
func (u *uiState) invalidate() {
	if u.win != nil {
		u.win.Invalidate()
	}
}

// statusSnapshot membaca status dan hasil ping untuk UI maupun mode --connect.
func (u *uiState) statusSnapshot() (string, string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.status, u.pingMs
}

func (u *uiState) start() {
	p := u.getActiveProfile()
	if p == nil || p.Address == "" || p.UUID == "" {
		u.status = "GAGAL: profil aktif belum lengkap."
		return
	}
	port, err := parsePort(p.Port)
	if err != nil {
		u.status = "GAGAL: port server tidak valid."
		return
	}
	if licenseExpired(p.LicenseExpire, time.Now()) {
		u.status = "GAGAL: lisensi profil ini sudah kedaluwarsa (" + p.LicenseExpire + ")."
		return
	}
	core, err := coreExecutable()
	if err != nil {
		u.status = "GAGAL: " + err.Error()
		return
	}
	// Alamat server dikunci ke IP sekarang, selagi DNS sistem masih normal.
	serverAddr, err := resolveServerAddress(p.Address, 6*time.Second)
	if err != nil {
		u.status = "GAGAL: hostname server tidak bisa diresolusi — " + err.Error()
		return
	}
	pinned := *p
	pinned.Address = serverAddr
	cfgBytes, err := buildSingboxConfig(pinned, port, coreModern(core))
	if err != nil {
		u.status = "GAGAL: pembuatan config sing-box gagal."
		return
	}
	appDir := appDataDir()
	if err := os.MkdirAll(appDir, 0700); err != nil {
		u.status = "GAGAL: folder data aplikasi tidak bisa dibuat."
		return
	}
	cfgFile := filepath.Join(appDir, "config.json")
	if err := os.WriteFile(cfgFile, cfgBytes, 0600); err != nil {
		u.status = "GAGAL: penulisan config.json gagal."
		return
	}

	cmd := exec.Command(core, "run", "-c", cfgFile)
	cmd.Dir = appDir
	setNoConsole(cmd)
	if err := cmd.Start(); err != nil {
		u.status = "GAGAL: core gagal start — " + err.Error()
		return
	}

	u.mu.Lock()
	u.cmd = cmd
	u.status = "Connecting..."
	u.pingMs = "Menunggu tunnel..."
	u.mu.Unlock()
	u.invalidate()

	go u.watchCore(cmd)
	go u.verifyTunnel(cmd)
}

// watchCore reports the session as dead when the core exits on its own.
func (u *uiState) watchCore(cmd *exec.Cmd) {
	err := cmd.Wait()
	u.mu.Lock()
	if u.cmd == cmd {
		u.cmd = nil
		if err != nil {
			u.status = "GAGAL: core berhenti — " + err.Error()
		} else {
			u.status = "Disconnected"
		}
		u.pingMs = "-"
	}
	u.mu.Unlock()
	u.stopPingLoop()
	u.invalidate()
}

// verifyTunnel only reports "Connected" after a real HTTP 204 has travelled
// through the TUN. That request also uses DNS, so it exercises the hijacked
// resolver: a green status means both the tunnel and its DNS path work.
func (u *uiState) verifyTunnel(cmd *exec.Cmd) {
	target := u.pingTarget()
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		time.Sleep(time.Duration(attempt) * 2 * time.Second)
		if !u.coreAlive(cmd) {
			return
		}
		duration, code, err := probeTunnel(target)
		if err == nil {
			u.mu.Lock()
			if u.cmd == cmd {
				u.status = "Connected"
				u.pingMs = fmt.Sprintf("%d ms (HTTP %d)", duration.Milliseconds(), code)
			}
			u.mu.Unlock()
			u.invalidate()
			if u.pingLoopEnabled() {
				u.startPingLoop()
			}
			return
		}
		lastErr = err
	}
	u.mu.Lock()
	dead := u.cmd == cmd
	if dead {
		u.status = "GAGAL: tunnel tidak menjawab — " + lastErr.Error()
		u.pingMs = "-"
	}
	u.mu.Unlock()
	u.invalidate()
	if dead {
		u.killCore()
	}
}

func (u *uiState) coreAlive(cmd *exec.Cmd) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.cmd == cmd
}

func (u *uiState) pingTarget() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.data.PingURL == "" {
		return defaultPingURL
	}
	return u.data.PingURL
}

func (u *uiState) pingLoopEnabled() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.data.HTTPPing
}

// killCore tears the core down without touching the status line.
func (u *uiState) killCore() {
	u.mu.Lock()
	cmd := u.cmd
	u.cmd = nil
	u.mu.Unlock()
	u.stopPingLoop()
	probeClient.CloseIdleConnections()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (u *uiState) stop() {
	u.killCore()
	u.mu.Lock()
	u.status = "Disconnected"
	u.pingMs = "-"
	u.mu.Unlock()
	u.invalidate()
}

func (u *uiState) startPingLoop() {
	u.stopPingLoop()
	stop := make(chan struct{})
	u.mu.Lock()
	u.stopPing = stop
	interval := time.Duration(u.data.PingInterval) * time.Second
	u.mu.Unlock()

	go func() {
		// Quick initial ping after 1 second
		select {
		case <-stop:
			return
		case <-time.After(1 * time.Second):
			u.execPing()
		}

		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				u.execPing()
			}
		}
	}()
}

func (u *uiState) stopPingLoop() {
	u.mu.Lock()
	stop := u.stopPing
	u.stopPing = nil
	u.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

func (u *uiState) execPing() {
	target := u.pingTarget()
	start := time.Now()
	_, code, err := probeTunnel(target)
	duration := time.Since(start)
	u.mu.Lock()
	if u.cmd != nil {
		if err == nil {
			u.pingMs = fmt.Sprintf("%d ms (HTTP %d)", duration.Milliseconds(), code)
		} else {
			u.pingMs = "Gagal (timeout/error)"
		}
	}
	u.mu.Unlock()
	u.invalidate()
}

// probeClient dipakai bersama semua probe: keep-alive membuat probe berikutnya
// memakai koneksi yang sama, bukan handshake TCP+TLS+WS baru tiap kali (dulu
// setiap probe meninggalkan koneksi baru ke server dan bikin CPU/handle naik).
var probeClient = &http.Client{
	Timeout: 6 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        2,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     120 * time.Second,
	},
}

// probeTunnel checks connectivity through the TUN, without a proxy: with
// auto_route every packet, including DNS, already travels the tunnel.
func probeTunnel(target string) (time.Duration, int, error) {
	client := probeClient
	start := time.Now()
	req, err := http.NewRequest(http.MethodHead, target, nil)
	if err != nil {
		return 0, 0, err
	}
	res, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return elapsed, 0, err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusOK {
		return elapsed, res.StatusCode, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return elapsed, res.StatusCode, nil
}

func appDataDir() string {
	// PANTUNNEL_DATA_DIR dipakai test dan dukungan teknis supaya bisa bekerja
	// tanpa menyentuh settings.json user.
	if dir := strings.TrimSpace(os.Getenv("PANTUNNEL_DATA_DIR")); dir != "" {
		return dir
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "."
	}
	return filepath.Join(d, "PanTunnel")
}

func configFilePath() string {
	return filepath.Join(appDataDir(), "settings.json")
}

func loadConfig() configData {
	b, err := os.ReadFile(configFilePath())
	if err != nil {
		return configData{
			HTTPPing:     true,
			PingInterval: defaultPingInterval,
			PingURL:      defaultPingURL,
		}
	}
	var cfg configData
	if json.Unmarshal(b, &cfg) != nil {
		return configData{
			HTTPPing:     true,
			PingInterval: defaultPingInterval,
			PingURL:      defaultPingURL,
		}
	}
	cfg.PingInterval = normalizePingInterval(cfg.PingInterval)
	if cfg.PingURL == "" {
		cfg.PingURL = defaultPingURL
	}
	return cfg
}

func saveConfig(cfg configData) {
	_ = os.MkdirAll(appDataDir(), 0700)
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err == nil {
		_ = os.WriteFile(configFilePath(), b, 0600)
	}
}

func parsePort(v string) (int, error) {
	p, err := strconv.Atoi(v)
	if err != nil || p < 1 || p > 65535 {
		return 0, fmt.Errorf("bad port")
	}
	return p, nil
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

func makeHWID(id string) string {
	h := sha256.Sum256([]byte(id))
	return strings.ToUpper(hex.EncodeToString(h[:]))[:24]
}
