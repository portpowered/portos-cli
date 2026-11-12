---
name: camera-look-around
description: Use this skill to look through a Ring pan-tilt camera with the port CLI, alone or while a call (for example a Muse voice call) is using the same camera. It covers holding the camera in shared mode, knowing where the camera points in degrees, turning it to an angle or an end in one command, sweeping the room for a panorama, taking snapshots, and releasing it.
license: Apache-2.0
compatibility: Needs the port CLI (signed in with `port auth login --device`), python3 for the helper, and a Ring camera on the account. Snapshots need ffmpeg on PATH.
metadata:
  author: Port OS Team
  last modified: 2026, november, 12
---

# Camera look around

A Ring camera allows one live view at a time. When a call already holds it, a plain `port session hold --camera` would take the camera away from the call. A shared hold attaches next to the call instead: Port OS keeps one live view and fans it out, so you and the call watch the same camera.

The camera turns left and right (pan) and up and down (tilt). Every turn answers with where the camera now points, in degrees, so you always know your orientation and can come back to anything you saw.

## Install

Get the skill files at the version of your CLI, and copy the starter tricks into your own directory:

```bash
VERSION=$(port --version | awk '{print $3}')        # for example v0.1.18
curl -fsSL "https://github.com/portpowered/portos-cli/archive/refs/tags/${VERSION}.tar.gz" | tar xz
mkdir -p ~/camera/notes
cp -rn "portos-cli-${VERSION#v}/skills/camera-look-around/tricks" ~/camera/
```

`~/camera/tricks/lib/portos_camera.py` is the starter helper (a `Camera` class with `turn`, `anchor`, `look_at`, `snapshot` and `panorama`). The two example tricks are `panorama` and `look-at`. Both are yours to improve.

## The rules

1. **Hold the camera in shared mode and keep the lease.** `port session hold <camera> --camera --shared --json` prints `{holdId, lease, endpointId, kinds, shared}`. Export `PORTOS_LEASE=<lease>` for every command after it.
2. **Look and turn only through `port`, with the lease.** Snapshots and turns come from your hold. A turn without the lease is refused (exit 13, `hold_lease_required`), because a turn moves the camera in your own session.
3. **Anchor first, then think in angles.** At the start of a camera session the angles count from wherever the camera happened to point. Turn to the left end and the bottom end once (`LEFT --degrees 360`, then `DOWN --degrees 360`, or `anchor()` in the helper). From then on pan is degrees right of the left end (`LEFT_END`) and tilt is degrees up from the bottom end (`BOTTOM_END`), and every angle you note stays valid for the rest of the session.
4. **Turn in one command, then look.** Ask for the whole turn at once with `--degrees`: the camera does it in one go and tells you where it ended. Do not loop single steps. Take a snapshot after a turn and describe what you see together with the angle.
5. **Keep a map of what is where.** Write down what you saw at each angle (`pan 170, tilt 40: the sofa`) in `~/camera/notes/`. To look at something again, turn to its angle; to answer "where is X", give the angle and the direction from the left end.
6. **You cannot speak.** A shared hold has no talkback: the call owns the speaker. `port message play-audio` is refused (exit 12, `shared_hold_no_talkback`). Leave the speaking to the call.
7. **The hold ends by itself.** A Ring camera ends its live view after about 12 minutes, which closes the shared session for every viewer, the call included. Your hold then becomes `ended` with reason `session_closed`. If you still need to look, hold the camera again (rule 1), export the new lease and anchor again: a new session counts angles from its own start. A session that runs to its own expiry (`camera.expiresAt` in `port session status --json`) ends with `session_expired`, and one whose camera is taken away ends with `preempted`. Check `port session status --json` whenever a command answers `hold_ended` or `camera_unavailable`.
8. **Always release at the end.** `port session release <holdId>` detaches you. The call keeps its live view. The camera closes about 30 seconds after the last viewer leaves.

## Orientation

Each `step-ptz` reply (with `--json`, under `position`) holds:

| Field | Meaning |
| --- | --- |
| `panDegrees` | Degrees right of the left end. Before the camera has reached an end it counts from the session start and goes negative to the left |
| `panReference` | `LEFT_END` once the camera has been turned to its left or right end in this session, `SESSION_START` before that |
| `panRangeDegrees` | Full pan travel, left end to right end |
| `tiltDegrees`, `tiltReference`, `tiltRangeDegrees` | The same for tilt, from the bottom end (`BOTTOM_END`) |
| `movedDegrees` | How far this turn actually went. Less than you asked means the camera stopped at an end |

Without `--json` the CLI prints the same as a sentence, for example `Turned ep/0abc LEFT 46°. Now pan 0° right of the left end (range 0 to 340°), tilt 15° below the session start (bottom end unknown until you turn to an end).`

Angles are counted from the turns the camera made, rounded to whole steps of about 15 degrees. Trust them to within a step. Anchoring again (turning to an end) corrects any drift.

## Commands

| Command | Does |
| --- | --- |
| `port endpoint list --type CAMERA --json` | Finds the camera's endpoint id |
| `port session hold <camera> --camera --shared --json` | Attaches next to the call and prints `{holdId, lease}` |
| `port session status --json` | Whether the camera hold is live, and when it expires |
| `port message snapshot <camera> --output view.jpg` | Saves the newest frame from your hold (about a second) |
| `port message step-ptz <camera> LEFT --degrees 60` | Turns about 60 degrees: `LEFT`, `RIGHT`, `UP` or `DOWN`. Without `--degrees` it turns one step |
| `port message step-ptz <camera> LEFT --degrees 360` | Turns all the way to that end. Other viewers keep their view |
| `port message record <camera> --output clip.mp4 --duration 10` | Records your hold's video (no sound) |
| `port session release <holdId>` | Detaches |

A turn takes about half a second per 15 degrees plus about half a second for the command, so a quarter turn is about 3 seconds and a full sweep from one end to the other about 12 seconds.

## Recipes

Set up once:

```bash
CAM=ep/...                                            # from port endpoint list
export PORTOS_LEASE=$(port session hold "$CAM" --camera --shared --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"])')
port message step-ptz "$CAM" LEFT --degrees 360       # anchor pan: now 0 = left end
port message step-ptz "$CAM" DOWN --degrees 360       # anchor tilt: now 0 = bottom end
```

**"Move all the way to the left."** One command, then look:

```bash
port message step-ptz "$CAM" LEFT --degrees 360 && port message snapshot "$CAM" --output left-end.jpg
```

**"Look up."** Tilt up by an amount that matches the request (a little: 20, clearly up: 45, all the way: 360):

```bash
port message step-ptz "$CAM" UP --degrees 45 && port message snapshot "$CAM" --output up.jpg
```

**"Scan left and right."** Sweep from the left end to the right end and keep a frame every 60 degrees or so:

```bash
python3 ~/camera/tricks/panorama/run.py "$CAM" --every 60 --out scan/
```

It prints `{"checkpoint": "panorama", "frames": [{"path", "pan", "tilt"}, ...]}`. Look at every frame in order and say what is in each and how the frames join up.

**"Look around and tell me what you see."** Run the panorama at eye level, describe the room from left to right with the angle of each thing, note those angles in `~/camera/notes/`, then point the camera at the most interesting thing:

```bash
python3 ~/camera/tricks/panorama/run.py "$CAM" --every 60 --tilt 40 --out room/
python3 ~/camera/tricks/look-at/run.py "$CAM" --pan 170 --tilt 40 --out sofa.jpg
```

**"Look at the window again."** Read the window's angle from your notes and turn there: `look_at(pan=..., tilt=...)` in the helper, or one `step-ptz` per axis with the difference from the current angle as `--degrees`.

In your own scripts:

```python
import sys; sys.path.insert(0, "/home/you/camera/tricks/lib")
from portos_camera import Camera

with Camera("ep/0abc") as camera:          # uses PORTOS_LEASE if set, else holds and releases
    camera.anchor()
    for pan in (0, 120, 240):
        camera.look_at(pan=pan, tilt=40)
        camera.snapshot("pan-%d.jpg" % pan)
    camera.checkpoint("looked")
```

## Refusals

| Exit | Code | Meaning and what to do |
| --- | --- | --- |
| 13 | `hold_lease_required` | The turn or snapshot had no lease. Pass `--lease` or set `PORTOS_LEASE` |
| 13 | `hold_ended` | The hold ended. `port session status --json` gives the reason; after `session_closed` or `session_expired`, hold the camera again and anchor again |
| 2 | `invalid_argument` | `--degrees` must be between 1 and 360 |
| 12 | `shared_hold_no_talkback` | Shared holds cannot play audio |
| 1 | `shared_session_full` or `shared_audio_publisher_busy` | At most four viewers share one camera; try again after one leaves |
| 14 | `camera_unavailable` | The shared session ended under you. Release and hold the camera again |
| 1 | `shared_session_unsupported` | This camera does not offer a shared session; use `--camera` without `--shared` only when no call is using it |
| 1 | `sfu_unreachable` | The fan-out service was not reachable; try again in a moment |
