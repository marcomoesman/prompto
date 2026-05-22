package attribution

import (
	"fmt"
	"sort"
	"strings"

	"github.com/marcomoesman/prompto/internal/version"
)

type ModuleNotice struct {
	Path    string
	Version string
	License string
	Note    string
}

var ThirdPartyModules = []ModuleNotice{
	{"charm.land/bubbles/v2", "v2.1.0", "MIT", ""},
	{"charm.land/bubbletea/v2", "v2.0.2", "MIT", ""},
	{"charm.land/lipgloss/v2", "v2.0.2", "MIT", ""},
	{"codeberg.org/readeck/go-readability/v2", "v2.1.1", "MIT", ""},
	{"github.com/JohannesKaufmann/dom", "v0.2.0", "MIT", ""},
	{"github.com/JohannesKaufmann/html-to-markdown/v2", "v2.5.0", "MIT", ""},
	{"github.com/alecthomas/chroma/v2", "v2.20.0", "MIT", ""},
	{"github.com/andybalholm/brotli", "v1.2.0", "MIT", ""},
	{"github.com/andybalholm/cascadia", "v1.3.3", "BSD-3-Clause", ""},
	{"github.com/araddon/dateparse", "v0.0.0-20210429162001-6b43995a97de", "MIT", ""},
	{"github.com/atotto/clipboard", "v0.1.4", "BSD-3-Clause", ""},
	{"github.com/aymanbagabas/go-osc52/v2", "v2.0.1", "MIT", ""},
	{"github.com/aymerick/douceur", "v0.2.0", "MIT", ""},
	{"github.com/bahlo/generic-list-go", "v0.2.0", "BSD-3-Clause", ""},
	{"github.com/bdandy/go-errors", "v1.2.2", "MIT", ""},
	{"github.com/bdandy/go-socks4", "v1.2.3", "MIT", ""},
	{"github.com/bogdanfinn/fhttp", "v0.6.8", "BSD-3-Clause", "The module does not include a top-level LICENSE file; its Go-derived source files carry Go Authors BSD-style license headers."},
	{"github.com/bogdanfinn/quic-go-utls", "v1.0.9-utls", "MIT", ""},
	{"github.com/bogdanfinn/tls-client", "v1.14.0", "BSD-4-Clause", "Advertising materials mentioning features or use of this software must include the acknowledgement printed below."},
	{"github.com/bogdanfinn/utls", "v1.7.7-barnius", "BSD-3-Clause", ""},
	{"github.com/bogdanfinn/websocket", "v1.5.5-barnius", "BSD-3-Clause", ""},
	{"github.com/buger/jsonparser", "v1.1.1", "MIT", ""},
	{"github.com/charmbracelet/colorprofile", "v0.4.2", "MIT", ""},
	{"github.com/charmbracelet/glamour", "v1.0.0", "MIT", ""},
	{"github.com/charmbracelet/lipgloss", "v1.1.1-0.20250404203927-76690c660834", "MIT", ""},
	{"github.com/charmbracelet/ultraviolet", "v0.0.0-20260205113103-524a6607adb8", "MIT", ""},
	{"github.com/charmbracelet/x/ansi", "v0.11.6", "MIT", ""},
	{"github.com/charmbracelet/x/cellbuf", "v0.0.15", "MIT", ""},
	{"github.com/charmbracelet/x/exp/slice", "v0.0.0-20250327172914-2fdc97757edf", "MIT", ""},
	{"github.com/charmbracelet/x/term", "v0.2.2", "MIT", ""},
	{"github.com/charmbracelet/x/termios", "v0.1.1", "MIT", ""},
	{"github.com/charmbracelet/x/windows", "v0.2.2", "MIT", ""},
	{"github.com/chromedp/cdproto", "v0.0.0-20260321001828-e3e3800016bc", "MIT", ""},
	{"github.com/chromedp/chromedp", "v0.15.1", "MIT", ""},
	{"github.com/chromedp/sysutil", "v1.1.0", "MIT", ""},
	{"github.com/clipperhouse/displaywidth", "v0.11.0", "MIT", ""},
	{"github.com/clipperhouse/uax29/v2", "v2.7.0", "MIT", ""},
	{"github.com/dlclark/regexp2", "v1.11.5", "MIT", ""},
	{"github.com/dustin/go-humanize", "v1.0.1", "MIT", ""},
	{"github.com/go-json-experiment/json", "v0.0.0-20260214004413-d219187c3433", "BSD-3-Clause", ""},
	{"github.com/go-shiori/dom", "v0.0.0-20230515143342-73569d674e1c", "MIT", ""},
	{"github.com/gobwas/httphead", "v0.1.0", "MIT", ""},
	{"github.com/gobwas/pool", "v0.2.1", "MIT", ""},
	{"github.com/gobwas/ws", "v1.4.0", "MIT", ""},
	{"github.com/gogs/chardet", "v0.0.0-20211120154057-b7413eaefb8f", "MIT", ""},
	{"github.com/google/uuid", "v1.6.0", "BSD-3-Clause", ""},
	{"github.com/gorilla/css", "v1.0.1", "BSD-3-Clause", ""},
	{"github.com/invopop/jsonschema", "v0.13.0", "MIT", ""},
	{"github.com/klauspost/compress", "v1.18.2", "BSD-3-Clause AND Apache-2.0", "The module license file includes additional Apache-2.0 coverage for gzhttp files."},
	{"github.com/lucasb-eyer/go-colorful", "v1.3.0", "MIT", ""},
	{"github.com/mailru/easyjson", "v0.7.7", "MIT", ""},
	{"github.com/mattn/go-isatty", "v0.0.20", "MIT", ""},
	{"github.com/mattn/go-runewidth", "v0.0.21", "MIT", ""},
	{"github.com/microcosm-cc/bluemonday", "v1.0.27", "BSD-3-Clause", ""},
	{"github.com/muesli/cancelreader", "v0.2.2", "MIT", ""},
	{"github.com/muesli/reflow", "v0.3.0", "MIT", ""},
	{"github.com/muesli/termenv", "v0.16.0", "MIT", ""},
	{"github.com/ncruces/go-strftime", "v1.0.0", "MIT", ""},
	{"github.com/quic-go/qpack", "v0.6.0", "MIT", ""},
	{"github.com/remyoudompheng/bigfft", "v0.0.0-20230129092748-24d4a6f8daec", "BSD-3-Clause", ""},
	{"github.com/rivo/uniseg", "v0.4.7", "MIT", ""},
	{"github.com/sabhiram/go-gitignore", "v0.0.0-20210923224102-525f6e181f06", "MIT", ""},
	{"github.com/tam7t/hpkp", "v0.0.0-20160821193359-2b70b4024ed5", "MIT", ""},
	{"github.com/wk8/go-ordered-map/v2", "v2.1.8", "Apache-2.0", ""},
	{"github.com/xo/terminfo", "v0.0.0-20220910002029-abceb7e1c41e", "MIT", ""},
	{"github.com/yuin/goldmark", "v1.7.13", "MIT", ""},
	{"github.com/yuin/goldmark-emoji", "v1.0.6", "MIT", ""},
	{"golang.org/x/crypto", "v0.46.0", "BSD-3-Clause", ""},
	{"golang.org/x/net", "v0.48.0", "BSD-3-Clause", ""},
	{"golang.org/x/sync", "v0.20.0", "BSD-3-Clause", ""},
	{"golang.org/x/sys", "v0.42.0", "BSD-3-Clause", ""},
	{"golang.org/x/term", "v0.38.0", "BSD-3-Clause", ""},
	{"golang.org/x/text", "v0.32.0", "BSD-3-Clause", ""},
	{"gopkg.in/yaml.v3", "v3.0.1", "MIT AND Apache-2.0", ""},
	{"modernc.org/libc", "v1.72.0", "BSD-3-Clause", ""},
	{"modernc.org/mathutil", "v1.7.1", "BSD-3-Clause", ""},
	{"modernc.org/memory", "v1.11.0", "BSD-3-Clause", ""},
	{"modernc.org/sqlite", "v1.49.1", "BSD-3-Clause", ""},
}

const bogdanFinnBSD4Notice = `BSD-4-Clause notice for github.com/bogdanfinn/tls-client:
Copyright (c) 2023, Bogdan Finn. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the four BSD-4-Clause
conditions are met. Advertising materials mentioning features or use of
this software must display the following acknowledgement:

This product includes software developed by Bogdan Finn.`

func RenderLicenseReport() string {
	mods := append([]ModuleNotice(nil), ThirdPartyModules...)
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].License != mods[j].License {
			return mods[i].License < mods[j].License
		}
		return mods[i].Path < mods[j].Path
	})

	var b strings.Builder
	fmt.Fprintf(&b, "prompto %s\n", version.Version)
	b.WriteString("License: Apache-2.0\n")
	b.WriteString("Project: https://github.com/marcomoesman/prompto\n")
	b.WriteString("Full prompto license text is in LICENSE.\n\n")
	b.WriteString("Third-party Go module notices\n")
	b.WriteString("These entries cover the modules required by go.mod. Release artifacts should include this report and the upstream license texts for the listed modules.\n\n")

	current := ""
	for _, m := range mods {
		if m.License != current {
			current = m.License
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			fmt.Fprintf(&b, "**%s**\n\n", current)
		}
		fmt.Fprintf(&b, "- %s %s", m.Path, m.Version)
		if m.Note != "" {
			fmt.Fprintf(&b, " — %s", m.Note)
		}
		b.WriteByte('\n')
	}

	b.WriteString("\n")
	b.WriteString(bogdanFinnBSD4Notice)
	b.WriteByte('\n')
	return b.String()
}
