package main

import "os/exec"

func openBrowser(url string) {
	// Try common openers in order
	for _, cmd := range []string{"xdg-open", "open", "sensible-browser"} {
		if err := exec.Command(cmd, url).Start(); err == nil {
			return
		}
	}
}
