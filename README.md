# razerctl

[![ci](https://github.com/Giftpilz0/razerctl/actions/workflows/ci.yml/badge.svg)](https://github.com/Giftpilz0/razerctl/actions/workflows/ci.yml)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Lighting and settings for the Razer DeathAdder V2 Pro and Mouse Dock on Linux: a terminal UI and a
user service that keeps the saved preset applied.

## Usage

```sh
curl -fsSLO https://github.com/Giftpilz0/razerctl/releases/latest/download/razerctl_linux_amd64.tar.gz
tar xzf razerctl_linux_amd64.tar.gz razerctl
./razerctl install   # binary to ~/.local/bin, udev rule (sudo), user service; rerun to upgrade
```

```sh
razerctl             # terminal UI
razerctl status      # device state (read-only)
razerctl apply       # apply the saved preset once
razerctl uninstall   # remove everything; --purge also deletes the settings
```

Service log: `journalctl --user -u razerctl -f`

## Development

Every push to `main` that passes CI is released as `v0.0.N`. Run `just` for all recipes and `just check` before committing. Conventions: [Giftpilz0/shared](https://github.com/Giftpilz0/shared).

## License

[MIT](LICENSE)
