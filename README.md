# OpenConnect Menubar

A small macOS menu bar app for [OpenConnect](https://www.infradead.org/openconnect/).
Keep several VPN profiles, switch between them in one click, and manage them in a
simple local page instead of shell scripts.

- Works with every openconnect protocol: AnyConnect, GlobalProtect, Pulse, F5, Fortinet, Array and Juniper
- Client certificates, username and password, server certificate pinning, auth groups
- DNS search domains
- Passwords stay in the macOS Keychain and are given to openconnect on stdin, so they never appear in `ps`

## Requirements

- macOS 13 or later
- Go 1.22+ and the Xcode Command Line Tools, only to build from source
- openconnect and vpnc-script from Homebrew (`brew install openconnect`) or MacPorts (`sudo port install openconnect`)

## Install

Download the latest zip from [Releases](https://github.com/yuriyskliarenko/openconnect-menubar/releases),
unzip it and move the app to `~/Applications`. It runs on Intel and Apple Silicon.

The app isn't notarized by Apple, so macOS blocks a downloaded copy. Clear the
quarantine flag once:

```sh
xattr -dr com.apple.quarantine ~/Applications/"OpenConnect Menubar.app"
```

Or build it yourself:

```sh
make install
open ~/Applications/"OpenConnect Menubar.app"
```

### Allow openconnect to run as root

openconnect has to create a network interface, and a menu bar app has no terminal
to type a sudo password into. On first launch the Manage Connections page shows
the exact command for your machine. It creates a rule like this one:

```
you ALL=(root) NOPASSWD: /opt/homebrew/bin/openconnect, /bin/kill
```

**This rule makes your account root-equivalent without a password.** openconnect
can run any command through `--script`, and `kill` can stop any process. Skip this
app if that isn't acceptable on your machine.

## Usage

Choose **Manage Connections…** in the menu to add a profile, then pick it from the
menu to connect. If a password isn't saved, you'll be asked for it when connecting.
If the server prompts for more than the fields provided, such as a one-time code,
the connection fails and the reason is in **Show Log**. Interactive two-factor
prompts aren't supported yet.

| Data | Location |
| --- | --- |
| Connections, no secrets | `~/Library/Application Support/openconnect-menubar/connections.json` |
| Passwords | Keychain, service `openconnect-menubar` |
| Log | `~/Library/Logs/openconnect-menubar.log` |

## Security

The manager page is served on a random `127.0.0.1` port. Every request must carry a
random token created at launch and a matching `Host` header, which keeps other
websites out. Connection values that reach a root shell are validated.

## Development

```sh
make test
go run . -serve    # manager page only, prints its URL
```

Pushing a `v*` tag runs the release workflow, which tests, builds a universal app
and publishes the zip. `make release` builds the same zip locally.

`OCM_CONFIG_DIR` overrides the config directory and makes the app print the manager URL.

## License

MIT
