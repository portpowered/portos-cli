# port

`port` is the command line interface for [Port OS](https://portoperatingsystem.com),
a cloud operating system for your connected devices: list and control them,
take snapshots, record clips, and drive robots under a hold.

This repository is a release copy of the CLI in the Port OS monorepo. Issues
are welcome; changes land upstream and are synced here.

## Install

Download the binary for your platform from the
[latest release](https://github.com/portpowered/portos-cli/releases/latest)
and check it against `SHA256SUMS`:

```bash
curl -fsSLO https://github.com/portpowered/portos-cli/releases/latest/download/port-linux-amd64
curl -fsSLO https://github.com/portpowered/portos-cli/releases/latest/download/SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
install -m 0755 port-linux-amd64 ~/.local/bin/port
```

Release assets are `port-<os>-<arch>` for `linux`, `darwin` and `windows`
(`.exe`) on `amd64` and `arm64`. With Go 1.25 or later you can also build from
source:

```bash
go install github.com/portpowered/portos-cli/cmd/port@latest
```

`port message record` needs `ffmpeg` on your `PATH`.

## Sign in

```bash
port auth login
```

opens a browser to sign in. On a machine that cannot open one (a remote shell,
a container, an agent sandbox), use a device code instead:

```bash
port auth login --device
```

The CLI prints a link and a code. Open the link in any browser where you are
signed in to Port OS, check that the code matches, and allow it. The CLI then
stores a login it refreshes on its own.

## Use

```bash
port endpoint list                        # your devices
port endpoint show <endpoint-id>          # what one device can do
port message snapshot <endpoint-id> --output ./frame.jpg
port message record <endpoint-id> --output ./clip.mp4 --duration 30
```

`record` saves video, with sound when the device sends it, for the requested
duration. `.mp4` and `.mov` encode the audio as AAC, `.mkv` copies both tracks,
and `.webm` holds VP8 video only.

Both commands connect through the Port OS media relay when a direct connection
is not possible. On a network without UDP, force the relay:

```bash
port message record <endpoint-id> --output ./clip.mp4 --duration 30 --relay-only
```

Run `port --help`, or `port <command> --help`, for every command and flag. Add
`--json` for machine-readable output.

## Drive a robot vacuum

`port session hold <endpoint-id> --motion` keeps a robot under remote control
between commands; every move after it carries the printed lease and comes back
measured. [`skills/robot-navigation`](skills/robot-navigation/SKILL.md) is an
Agent Skill for agents that drive robots this way: the rules, a stdlib-only
Python helper that corrects moves from the measured replies, a map renderer and
two example tricks.

## Look through a camera next to a call

`port session hold <endpoint-id> --camera --shared` attaches to a Ring camera
that a call already uses, instead of taking it away. Snapshots, recordings and
`port message step-ptz <endpoint-id> LEFT|RIGHT|UP|DOWN` then work under the
printed lease. [`skills/camera-look-around`](skills/camera-look-around/SKILL.md)
is an Agent Skill for agents that look around this way.

## Build and test

```bash
CGO_ENABLED=0 go build -o port ./cmd/port
CGO_ENABLED=0 go test ./...
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
