//go:build !windows

package main

import "fmt"

func readClipboard() string { return "" }

func writeClipboard(string) error {
	return fmt.Errorf("clipboard belum didukung pada platform ini")
}
func chooseImportFile() (string, error) {
	return "", fmt.Errorf("file picker belum tersedia pada platform ini")
}
