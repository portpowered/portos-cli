---
name: camera-look-around
description: Use this skill to look through a Ring camera with the port CLI while a call (for example a Muse voice call) is using the same camera. It covers holding the camera in shared mode, taking snapshots, stepping the camera left, right, up and down, and releasing it.
license: Apache-2.0
compatibility: Needs the port CLI (signed in with `port auth login --device`) and a Ring camera on the account. Snapshots need ffmpeg on PATH.
metadata:
  author: Port OS Team
  last modified: 2026, november, 11
---

# Camera look around

A Ring camera allows one live view at a time. When a call already holds it, a plain `port session hold --camera` would take the camera away from the call. A shared hold attaches next to the call instead: Port OS keeps one live view and fans it out, so you and the call watch the same camera.

## The rules

1. **Hold the camera in shared mode and keep the lease.** `port session hold <camera> --camera --shared --json` prints `{holdId, lease, endpointId, kinds, shared}`. Export `PORTOS_LEASE=<lease>` for every command after it.
2. **Look and step only through `port`, with the lease.** Snapshots and steps come from your hold. A step without the lease is refused (exit 13, `hold_lease_required`), because a step moves the camera in your own session.
3. **Step, then snapshot, then judge.** One step is a small turn. After each step take a snapshot and describe what changed before you step again. Do not chain steps blind.
4. **You cannot speak.** A shared hold has no talkback: the call owns the speaker. `port message play-audio` is refused (exit 12, `shared_hold_no_talkback`). Leave the speaking to the call.
5. **The hold ends by itself.** A shared session lasts about an hour from the first attach and is not renewed past that. `port session status --json` shows `camera.expiresAt`. At the end the hold becomes `ended` with reason `session_expired`. The hold also ends when the call's session is closed or the camera is taken away.
6. **Always release at the end.** `port session release <holdId>` detaches you. The call keeps its live view. The camera closes about 30 seconds after the last viewer leaves.

## Commands

| Command | Does |
| --- | --- |
| `port endpoint list --type CAMERA --json` | Finds the camera's endpoint id |
| `port session hold <camera> --camera --shared --json` | Attaches next to the call and prints `{holdId, lease}` |
| `port session status --json` | Whether the camera hold is live, and when it expires |
| `port message snapshot <camera> --output view.jpg` | Saves the newest frame from your hold (about a second) |
| `port message step-ptz <camera> LEFT` | One step: `LEFT`, `RIGHT`, `UP` or `DOWN`. Works for any attached viewer, and others keep their view |
| `port message record <camera> --output clip.mp4 --duration 10` | Records your hold's video (no sound) |
| `port session release <holdId>` | Detaches |

## Looking around

```bash
CAM=ep/...                                            # from port endpoint list
export PORTOS_LEASE=$(port session hold "$CAM" --camera --shared --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["lease"])')
port message snapshot "$CAM" --output front.jpg       # look first
port message step-ptz "$CAM" LEFT
port message snapshot "$CAM" --output left.jpg        # then compare with front.jpg
port message step-ptz "$CAM" RIGHT
port message step-ptz "$CAM" RIGHT
port message snapshot "$CAM" --output right.jpg
port session release "$(port session status --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["holds"][0]["id"])')"
```

Describe each snapshot in words and say what moved between two of them. Steps are provider-defined, so do not promise an angle. Two steps in opposite directions return the camera to about where it was.

## Refusals

| Exit | Code | Meaning and what to do |
| --- | --- | --- |
| 13 | `hold_lease_required` | The step or snapshot had no lease. Pass `--lease` or set `PORTOS_LEASE` |
| 13 | `hold_ended` | The hold ended (see `port session status`). Hold the camera again |
| 12 | `shared_hold_no_talkback` | Shared holds cannot play audio |
| 1 | `shared_session_full` or `shared_audio_publisher_busy` | At most four viewers share one camera; try again after one leaves |
| 14 | `camera_unavailable` | The shared session ended under you. Release and hold the camera again |
| 1 | `shared_session_unsupported` | This camera does not offer a shared session; use `--camera` without `--shared` only when no call is using it |
| 1 | `sfu_unreachable` | The fan-out service was not reachable; try again in a moment |
