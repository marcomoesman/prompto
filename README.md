# Prompto
Lightweight coding agent that is capable but optimized for smaller local models. Ideal for small models such as Qwen 3.5 9B, Qwen 3.6 27B and Qwen 3.6 35B.

---

## Install

### macOS / Linux

```bash
curl -fsSL https://raw.githubusercontent.com/marcomoesman/prompto/main/install.sh | bash
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/marcomoesman/prompto/main/install.ps1 | iex
```

The installer downloads the latest release, verifies its SHA-256
checksum against the release's `checksums.txt`, drops the `prompto`
binary in a user-local directory (`~/.local/bin` on Unix,
`%LOCALAPPDATA%\Programs\prompto` on Windows), adds it to your PATH,
and walks you through a minimal config (cloud key or local model).

Re-running the installer is the upgrade path: it compares the
installed version against the latest release and only replaces the
binary when they differ.

To pin a version, download the script and invoke it explicitly:

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/marcomoesman/prompto/main/install.sh
bash install.sh --version v0.1.0
```

```powershell
irm https://raw.githubusercontent.com/marcomoesman/prompto/main/install.ps1 -OutFile install.ps1
.\install.ps1 -Version v0.1.0
```

To uninstall:

```bash
bash install.sh --uninstall    # macOS / Linux
.\install.ps1 -Uninstall       # Windows
```

Manual installs: each release on the
[Releases page](https://github.com/marcomoesman/prompto/releases)
publishes archives for `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`,
and `windows/amd64`, plus a `checksums.txt`.

Configuration lives at `~/.config/prompto/config.json`
(`%AppData%\prompto\config.json` on Windows). See
[`docs/CONFIG.md`](docs/CONFIG.md) for the full schema.

---

## Third-party notices

prompto's `webfetch` tool emits a Chrome-shaped TLS ClientHello and
HTTP/2 SETTINGS via [bogdanfinn/tls-client](https://github.com/bogdanfinn/tls-client).
Run `/license` in the TUI for prompto's own license plus the current
third-party Go module notice list.

`github.com/bogdanfinn/tls-client` is BSD-4-Clause and requires this
advertising acknowledgement when advertising materials mention features
or use of that software:

> Copyright (c) 2023, Bogdan Finn. All rights reserved.
>
> Redistribution and use in source and binary forms, with or without
> modification, are permitted provided that the following conditions are
> met:
>
> 1. Redistributions of source code must retain the above copyright
>    notice, this list of conditions and the following disclaimer.
> 2. Redistributions in binary form must reproduce the above copyright
>    notice, this list of conditions and the following disclaimer in the
>    documentation and/or other materials provided with the distribution.
> 3. All advertising materials mentioning features or use of this software
>    must display the following acknowledgement: This product includes
>    software developed by Bogdan Finn.
> 4. Neither the name of the copyright holder nor the names of its
>    contributors may be used to endorse or promote products derived from
>    this software without specific prior written permission.

Release artifacts include this attribution and `THIRD_PARTY_NOTICES.md`
alongside prompto's own license.
