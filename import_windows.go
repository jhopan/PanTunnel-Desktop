//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	comdlg32             = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	procGetSaveFileNameW = comdlg32.NewProc("GetSaveFileNameW")
)

func readClipboard() string {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", "Get-Clipboard -Raw")
	setNoConsole(cmd)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// writeClipboard menaruh teks di clipboard lewat PowerShell (tanpa dependency baru).
func writeClipboard(text string) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", "[Console]::In.ReadToEnd().Trim() | Set-Clipboard")
	cmd.Stdin = strings.NewReader(text)
	setNoConsole(cmd)
	return cmd.Run()
}

type openFileNameW struct {
	lStructSize       uint32
	hwndOwner         uintptr
	hInstance         uintptr
	lpstrFilter       *uint16
	lpstrCustomFilter *uint16
	nMaxCustFilter    uint32
	nFilterIndex      uint32
	lpstrFile         *uint16
	nMaxFile          uint32
	lpstrFileTitle    *uint16
	nMaxFileTitle     uint32
	lpstrInitialDir   *uint16
	lpstrTitle        *uint16
	flags             uint32
	nFileOffset       uint16
	nFileExtension    uint16
	lpstrDefExt       *uint16
	lCustData         uintptr
	lpfnHook          uintptr
	lpTemplateName    *uint16
	pvReserved        uintptr
	dwReserved        uint32
	flagsEx           uint32
}

const (
	ofnFileMustExist   = 0x00001000
	ofnPathMustExist   = 0x00000800
	ofnOverwritePrompt = 0x00000002
	ofnNoChangeDir     = 0x00000008
)

func chooseImportFile() (string, error) {
	var filter []uint16
	for _, part := range []string{
		"Konfigurasi VLESS (*.txt;*.json;*.conf;*.jps;*.pantunnel)",
		"*.txt;*.json;*.conf;*.jps;*.pantunnel",
		"Semua File (*.*)",
		"*.*",
	} {
		filter = append(filter, utf16.Encode([]rune(part))...)
		filter = append(filter, 0)
	}
	filter = append(filter, 0)

	fileBuf := make([]uint16, 1024)

	var ofn openFileNameW
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	ofn.lpstrFilter = &filter[0]
	ofn.lpstrFile = &fileBuf[0]
	ofn.nMaxFile = uint32(len(fileBuf))
	ofn.lpstrTitle = syscall.StringToUTF16Ptr("Pilih File Konfigurasi")
	ofn.flags = ofnFileMustExist | ofnPathMustExist

	ret, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		return "", fmt.Errorf("dibatalkan")
	}

	var length int
	for length = 0; length < len(fileBuf); length++ {
		if fileBuf[length] == 0 {
			break
		}
	}
	return string(utf16.Decode(fileBuf[:length])), nil
}

// chooseSaveFile menanyakan nama file tujuan (dipakai ekspor lisensi .pantunnel).
func chooseSaveFile(defaultName string) (string, error) {
	var filter []uint16
	for _, part := range []string{"Lisensi PanTunnel (*.pantunnel)", "*.pantunnel", "Semua File (*.*)", "*.*"} {
		filter = append(filter, utf16.Encode([]rune(part))...)
		filter = append(filter, 0)
	}
	filter = append(filter, 0)

	fileBuf := make([]uint16, 1024)
	for i, r := range utf16.Encode([]rune(defaultName)) {
		if i >= len(fileBuf)-1 {
			break
		}
		fileBuf[i] = r
	}

	var ofn openFileNameW
	ofn.lStructSize = uint32(unsafe.Sizeof(ofn))
	ofn.lpstrFilter = &filter[0]
	ofn.lpstrFile = &fileBuf[0]
	ofn.nMaxFile = uint32(len(fileBuf))
	ofn.lpstrTitle = syscall.StringToUTF16Ptr("Simpan Lisensi")
	ofn.nFilterIndex = 1
	ofn.flags = ofnOverwritePrompt | ofnPathMustExist | ofnNoChangeDir

	ret, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if ret == 0 {
		return "", fmt.Errorf("dibatalkan")
	}
	var length int
	for length = 0; length < len(fileBuf); length++ {
		if fileBuf[length] == 0 {
			break
		}
	}
	return string(utf16.Decode(fileBuf[:length])), nil
}
