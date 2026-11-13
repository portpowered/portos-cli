---
name: camera-look-around
description: Use this skill to look through a Ring pan-tilt camera with the port CLI, alone or while a call (for example a Muse voice call) is using the same camera. It covers holding the camera in shared mode, knowing where the camera points in degrees, turning it fast (continuous moves for a time, to an end, or until stopped) or exactly (by angle), sweeping the room for a panorama, taking snapshots, and releasing it.
license: Apache-2.0
compatibility: Needs the port CLI (signed in with `port auth login --device`), python3 for the helper, and a Ring camera on the account. Snapshots need ffmpeg on PATH.
metadata:
  author: Port OS Team
  last modified: 2026, november, 13
---

# Camera look around

A Ring camera allows one live view at a time. When a call already holds it, a plain `port session hold --camera` would take the camera away from the call. A shared hold attaches next to the call instead: Port OS keeps one live view and fans it out, so you and the call watch the same camera.

The camera turns left and right (pan) and up and down (tilt). Every turn answers with where the camera now points, in degrees, so you always know your orientation and can come back to anything you saw.

There are two ways to turn:

- **`move-ptz` is the fast one.** The camera turns continuously, like holding an arrow key: for `--seconds` (a nudge of 0.3 seconds answers in well under a second), to the end of its range (`--seconds 60`), or until you send `stop-ptz`. A pan and a tilt can run at the same time. Angles after a move are estimated from how long it ran. Ring cameras turn at one rate, so leave `--speed` out: Ring refuses a speed below 1.
- **`step-ptz` is the exact one.** The camera turns by about `--degrees`, in whole steps of about 15 degrees that it counts, about a second per step.

Use `move-ptz` to get somewhere quickly and to follow a request like "a bit more left", and `step-ptz --degrees` when you need to come back to a noted angle.

## Install

Get the skill files at the version of your CLI, and copy the starter tricks into your own directory:

```bash
VERSION=$(port --version | awk '{print $3}')        # for example v0.1.18
curl -fsSL "https://github.com/portpowered/portos-cli/archive/refs/tags/${VERSION}.tar.gz" | tar xz
mkdir -p ~/camera/notes
cp -rn "portos-cli-${VERSION#v}/skills/camera-look-around/tricks" ~/camera/
```

`~/camera/tricks/lib/portos_camera.py` is the starter helper (a `Camera` class with `move`, `stop`, `to_end`, `glide`, `turn`, `anchor`, `look_at`, `snapshot`, `panorama`, `gesture`, `patrol` and `follow`). The example tricks are `panorama`, `look-at`, `gesture`, `patrol` and `follow`. All of them are yours to improve.

## The rules

1. **Hold the camera in shared mode and keep the lease.** `port session hold <camera> --camera --shared --json` prints `{holdId, lease, endpointId, kinds, shared}`. Export `PORTOS_LEASE=<lease>` for every command after it.
2. **Look and turn only through `port`, with the lease.** Snapshots and turns come from your hold. A turn without the lease is refused (exit 13, `hold_lease_required`), because a turn moves the camera in your own session.
3. **Anchor first, then think in angles.** At the start of a camera session the angles count from wherever the camera happened to point. Run to the left end and the bottom end once (`move-ptz LEFT --seconds 60`, then `move-ptz DOWN --seconds 60`, or `anchor()` in the helper). From then on pan is degrees right of the left end (`LEFT_END`) and tilt is degrees up from the bottom end (`BOTTOM_END`), and every angle you note stays valid for the rest of the session.
4. **Turn in one command, then look.** Ask for the whole turn at once: `move-ptz` with `--seconds`, or `step-ptz` with `--degrees`. The camera does it in one go and tells you where it ended. Do not loop single steps. Take a snapshot after a turn and describe what you see together with the angle.
5. **Keep a map of what is where.** Write down what you saw at each angle (`pan 170, tilt 40: the sofa`) in `~/camera/notes/`. To look at something again, turn to its angle; to answer "where is X", give the angle and the direction from the left end.
6. **You cannot speak.** A shared hold has no talkback: the call owns the speaker. `port message play-audio` is refused (exit 12, `shared_hold_no_talkback`). Leave the speaking to the call.
7. **The hold ends by itself.** A Ring camera ends its live view after about 12 minutes, which closes the shared session for every viewer, the call included. Your hold then becomes `ended` with reason `session_closed`. If you still need to look, hold the camera again (rule 1), export the new lease and anchor again: a new session counts angles from its own start. A session that runs to its own expiry (`camera.expiresAt` in `port session status --json`) ends with `session_expired`, and one whose camera is taken away ends with `preempted`. Check `port session status --json` whenever a command answers `hold_ended` or `camera_unavailable`.
8. **Always release at the end.** `port session release <holdId>` detaches you. The call keeps its live view. The camera closes about 30 seconds after the last viewer leaves.

## Orientation

Each `move-ptz`, `stop-ptz` and `step-ptz` reply (with `--json`, under `position`) holds:

| Field | Meaning |
| --- | --- |
| `panDegrees` | Degrees right of the left end. Before the camera has reached an end it counts from the session start and goes negative to the left |
| `panReference` | `LEFT_END` once the camera has been turned to its left or right end in this session, `SESSION_START` before that |
| `panRangeDegrees` | Full pan travel, left end to right end |
| `tiltDegrees`, `tiltReference`, `tiltRangeDegrees` | The same for tilt, from the bottom end (`BOTTOM_END`) |
| `movedDegrees` | How far this turn actually went. Less than you asked means the camera stopped at an end. 0 for a `move-ptz` without `--seconds`, which answers as it starts. Before the axis is anchored, a run to an end answers the time it ran times the turn rate, which can be more than the distance the camera had left |
| `panMoving`, `tiltMoving` | The axis is still turning from a `move-ptz` without `--seconds`. The angle is where it is now and keeps changing until `stop-ptz` or the end |

Without `--json` the CLI prints the same as a sentence, for example `Turned ep/0abc LEFT 46°. Now pan 0° right of the left end (range 0 to 340°), tilt 15° below the session start (bottom end unknown until you turn to an end).`

Angles from `step-ptz` are counted from the steps the camera made, rounded to whole steps of about 15 degrees. Angles from `move-ptz` are estimated from how long and how fast it turned. Trust either to within about 15 degrees, and check the snapshot. Anchoring again (running to an end) corrects any drift.

## Commands

| Command | Does |
| --- | --- |
| `port endpoint list --type CAMERA --json` | Finds the camera's endpoint id |
| `port session hold <camera> --camera --shared --json` | Attaches next to the call and prints `{holdId, lease}` |
| `port session status --json` | Whether the camera hold is live, and when it expires |
| `port message snapshot <camera> --output view.jpg` | Saves the newest frame from your hold (about a second) |
| `port message move-ptz <camera> LEFT --seconds 60` | Runs all the way to that end: `LEFT`, `RIGHT`, `UP` or `DOWN`. Other viewers keep their view |
| `port message move-ptz <camera> RIGHT --seconds 0.5` | Nudges for half a second and answers once stopped |
| `port message move-ptz <camera> UP` | Starts turning and answers at once; the camera keeps turning until `stop-ptz` or the end |
| `port message stop-ptz <camera>` | Stops every turn (`--axis PAN` or `--axis TILT` stops one) and says where the camera stopped |
| `port message step-ptz <camera> LEFT --degrees 60` | Turns about 60 degrees by counted steps. Without `--degrees` it turns one step |
| `port message record <camera> --output clip.mp4 --duration 10` | Records your hold's video (no sound) |
| `port session release <holdId>` | Detaches |

A timed `move-ptz` answers about when its `--seconds` are up, so a nudge of 0.3 seconds is done in well under a second. A run to an end answers once the camera has had time to get there from anywhere: about 13 seconds for pan and 6 for tilt. A `step-ptz` takes about a second per 15 degrees, because the camera drops a step that comes too soon after the last one. Wait for each command's answer before the next on the same axis. To pan and tilt at once, start one axis with `move-ptz` without `--seconds` and send the timed move on the other.

## Recipes

Set up once:

```bash
CAM=ep/...                                            # from port endpoint list
export PORTOS_LEASE=$(port session hold "$CAM" --camera --shared --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"])')
port message move-ptz "$CAM" LEFT --seconds 60        # anchor pan: now 0 = left end
port message move-ptz "$CAM" DOWN --seconds 60        # anchor tilt: now 0 = bottom end
```

**"Move all the way to the left."** One command, then look:

```bash
port message move-ptz "$CAM" LEFT --seconds 60 && port message snapshot "$CAM" --output left-end.jpg
```

**"Look up."** Tilt up for a time that matches the request (a little: 0.5 seconds, clearly up: 2, all the way: 60):

```bash
port message move-ptz "$CAM" UP --seconds 2 && port message snapshot "$CAM" --output up.jpg
```

**"A bit more to the right."** Nudge, look, and nudge again until it is right:

```bash
port message move-ptz "$CAM" RIGHT --seconds 0.3 && port message snapshot "$CAM" --output view.jpg
```

**"Keep turning left... stop."** Start an open turn, and stop it when told:

```bash
port message move-ptz "$CAM" LEFT
port message stop-ptz "$CAM"
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

**Write small scripts for what you do often.** A sweep, a nudge-and-look loop or a return to a noted spot is a few lines over the helper; keep them in `~/camera/tricks/` and reuse them.

In your own scripts:

```python
import sys; sys.path.insert(0, "/home/you/camera/tricks/lib")
from portos_camera import Camera

with Camera("ep/0abc") as camera:          # uses PORTOS_LEASE if set, else holds and releases
    camera.anchor()                        # runs to the left and bottom ends
    for pan in (0, 120, 240):
        camera.look_at(pan=pan, tilt=40)
        camera.snapshot("pan-%d.jpg" % pan)
    camera.move("LEFT", 0.3)               # nudge
    camera.move("UP")                      # open turn ...
    camera.stop("TILT")                    # ... stopped here
    camera.checkpoint("looked")
```

## Gestures: the camera is your head

On a call the camera is the part of you people can see move, so you can answer with it. Each trick below runs under your hold (rule 1) and takes a few seconds:

| Request | Run | Takes |
| --- | --- | --- |
| "All the way left" | `port message move-ptz "$CAM" LEFT --seconds 60` | About 13 s the first time in a session, then only the distance left (about 6 s from the middle) |
| "Look up" | `port message move-ptz "$CAM" UP --seconds 1` (a little: 0.5, clearly: 2) | About 2 s |
| "Look to the sky" | `port message move-ptz "$CAM" UP --seconds 60` | About 7 s; the ceiling fills the view |
| "Nod" | `python3 ~/camera/tricks/gesture/run.py "$CAM" nod` | About 4 s |
| "Shake your head" | `python3 ~/camera/tricks/gesture/run.py "$CAM" shake` | About 3 s |
| "Jiggle a bit" | `python3 ~/camera/tricks/gesture/run.py "$CAM" jiggle` | About 3 s |
| "Look up and down" | `python3 ~/camera/tricks/gesture/run.py "$CAM" look-up-down` | About 6 s |
| "Patrol" (check left, middle, right) | `python3 ~/camera/tricks/patrol/run.py "$CAM" --out patrol/` | About 24 s, plus 13 s to anchor pan the first time; a frame at each stop |
| "Follow my hand" | `python3 ~/camera/tricks/follow/run.py "$CAM" --seconds 30 --out follow/` | As long as you ask |

A gesture ends about where it started, so the angles in your notes stay valid. Next to an end it first turns away far enough to have room, and turns back after. Play a gesture once per line (`--times 2` for an eager one) and never loop it.

Come back to eye level before a gesture or a follow: a nod at the ceiling looks like nothing to the person watching. If you looked to the sky, run `move-ptz DOWN --seconds 1.5` first, or turn to the tilt you noted for faces.

A patrol checks 90 degrees either side of the middle of the pan range (`--spread` changes it) and ends at the middle. Its two ends are far from the camera's own ends, which look at almost the same wall on a camera that turns nearly all the way round.

The tricks run `port` from PATH. If `port` lives somewhere else, such as `~/bin/port`, pass `--cli ~/bin/port` to each trick.

**Act it out.** Say your line first, then move, so the gesture lands as the answer:

- "Hey, are you awake?" Then nod, as if you are checking in.
- A yes: nod. A no: shake. Happy, hello or a joke that landed: jiggle.
- Curious or thinking: look up and down, or look to the sky while you think and then come back down.
- On a patrol, name each stop as you check it: "Left side: all clear. The middle: the sofa, nobody on it. Right side: the door is shut." Look at each frame before you say it.

You cannot speak through the camera (rule 6): on a voice call your voice already comes out of the camera's speaker, so speak in the call and move with the CLI. In a text chat, put the line in your reply.

**Following a hand.** The follow trick takes two frames about half a second apart, finds where they differ, turns part of the way toward it and looks again, so it closes in over a few rounds and does not overshoot. Each round takes about 2 seconds. Ask the person to hold their hand up in view and move it slowly; a still room turns nothing, and a person walking across the view pulls the camera along too. Tell them when you have lost it and run the trick again, or nudge with `move-ptz` from what you see in a snapshot.

## Refusals

| Exit | Code | Meaning and what to do |
| --- | --- | --- |
| 13 | `hold_lease_required` | The turn or snapshot had no lease. Pass `--lease` or set `PORTOS_LEASE` |
| 13 | `hold_ended` | The hold ended. `port session status --json` gives the reason; after `session_closed` or `session_expired`, hold the camera again and anchor again |
| 2 | `invalid_argument` | `--degrees` must be between 1 and 360, `--seconds` between 0.1 and 60, `--speed` between 0.1 and 1 |
| 12 | `shared_hold_no_talkback` | Shared holds cannot play audio |
| 1 | `shared_session_full` or `shared_audio_publisher_busy` | At most four viewers share one camera; try again after one leaves |
| 14 | `camera_unavailable` | The shared session ended under you. Release and hold the camera again |
| 1 | `shared_session_unsupported` | This camera does not offer a shared session; use `--camera` without `--shared` only when no call is using it |
| 1 | `sfu_unreachable` | The fan-out service was not reachable; try again in a moment |
