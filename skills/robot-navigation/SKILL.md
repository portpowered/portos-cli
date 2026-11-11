---
name: robot-navigation
description: Use this skill to drive a robot vacuum around the house with the port CLI. It covers holding the robot, scripting ordered moves that correct themselves from measured replies, looking with the camera, and keeping tricks (scripts you validate and reuse) and house notes between conversations.
license: Apache-2.0
compatibility: Needs the port CLI (signed in with `port auth login --device`), python3 and a shell. Optional ffmpeg for audio and video.
metadata:
  author: Port OS Team
  last modified: 2026, november, 11
---

# Robot navigation

You drive the robot by running the `port` CLI from scripts in your own machine. The CLI's session host keeps the robot under remote control while you think, and every move comes back measured: where the robot is now, how far it went and how far it turned. You correct from those numbers. You never guess.

Everything you learn about the house goes into files you keep: places, routes, traps, calibration, and tricks (scripts plus a card each). Read them first every time.

## Install

Get the skill files at the version of your CLI, and copy the starter tricks into your own directory:

```bash
VERSION=$(port --version | awk '{print $3}')        # for example v0.1.3
curl -fsSL "https://github.com/portpowered/portos-cli/archive/refs/tags/${VERSION}.tar.gz" | tar xz
mkdir -p ~/robot/house
cp -rn "portos-cli-${VERSION#v}/skills/robot-navigation/tricks" ~/robot/
```

`~/robot/tricks/lib/portos_tricks.py` is the starter helper and `~/robot/tricks/lib/render_map.py` draws maps. Both are yours to improve. The two example tricks are `back-and-forth` and `look-around`.

## The rules

1. **Read your notes first.** `~/robot/house/` and every `~/robot/tricks/*/trick.yaml`. A kept trick that does the job beats a new script.
2. **Hold the robot, keep the lease, and send robot commands only through `port`.** `port session hold <robot> --motion --json` prints `{holdId, lease}`. Export `PORTOS_LEASE=<lease>` for every command after it. While you hold the robot, never call the Port OS API or the robot's app directly. The hold refuses commands without the lease (exit 13, `hold_lease_required`), so two scripts cannot interleave moves.
3. **Script ordered operations and check every measured reply.** Send one move, read the reply, work out what is left, send the rest. Asked for 90 degrees and got 84: turn 6 more. Asked to drive to a point and the robot bent around a chair: re-aim from the new pose and drive the rest. Heading is good to about 5 degrees, so re-aim every metre.
4. **Hand back wherever a photo must be judged.** A script cannot judge a frame and a photo never gives coordinates. Stop at a checkpoint that prints the frames and the pose (`robot.checkpoint(...)`), look at them yourself, and run the next step.
5. **Validate a new trick at least twice before you keep it.** Compare every step's measured reply with what the step meant, within the trick's tolerance. Only then set `validated` on its card. After every later run, update `runs` and `successes`: a trick that starts failing (a moved chair, a remapped house) shows it.
6. **Write down what the house teaches before moving on.** A trap, a calibration offset, a better route: into `~/robot/house/` now, not later.
7. **Always release at the end.** `port session release <holdId>` ends the session and sends the robot to its dock. The helper's `with Robot(...)` block releases even when the trick fails.

## Moving

| Command | Does |
| --- | --- |
| `port session hold <robot> --motion --json` | Drives the robot off its dock into remote control and prints `{holdId, lease}`. Ends by itself after `--dead-man` (10 minutes) with no command |
| `port session status --json` | The holds, their state, and why an ended hold ended |
| `port session release <holdId>` | Ends the session; the robot returns to its dock |
| `port message remote-control/move <robot> --wait --body '{"velocity":0.2,"omega":0,"duration":5000}' --json` | One move. The reply body has `pose`, `distanceMetres` and `turnedDegrees` |
| `port message move <robot> "rotate 90, forward 1m"` | The same moves from a short grammar, for one-off use |
| `port endpoint attribute <robot> spatial/pose --force` | Reads the pose from the robot |
| `port message snapshot <robot> --output frame.jpg` | Saves the camera's current frame |
| `port group list --expand interfaces.attributes --json` | Your groups; each room's `spatial/geometry` attribute holds its shape and map frame. Follow `next_token` for more pages |

A move: `velocity` in m/s (negative reverses), `omega` in rad/s (positive turns left), `duration` in whole seconds as milliseconds (1000 to 5000 on the S8). Distance is velocity times duration; the turn is omega times duration. The robot rejects a drive under 5 cm and a turn in place under 5 degrees. The pose is metres in the map's frame with heading 0 along +x, growing counter-clockwise.

The session host sends a zero-motion keepalive while you think, so the robot stays under remote control between your commands. The host owns `seqNum`; send any value.

Exit codes let a script branch without reading text:

| Exit | Means | Do |
| --- | --- | --- |
| 0 | done | read the measured reply |
| 10 | the move was not carried out (obstacle, wall, dock) | read the pose, change direction or go around |
| 11 | the session is not active (the robot docked) | hold again |
| 12 | busy (another move or clip) | wait for it, then resend |
| 13 | hold problem: no lease, another hold's lease, or the hold ended | `port session status` says why; hold again if it ended |
| 14 | camera unavailable | try again shortly, or work without the frame |
| 2 | invalid request | fix the request |
| 3 | not signed in | `port auth login --device` |

## The starter helper

```python
import sys; sys.path.insert(0, "/home/you/robot/tricks/lib")
from portos_tricks import Robot, NotCarriedOut

with Robot("<robot endpoint id>") as robot:   # holds; leaving the block releases (docks)
    start = robot.pose()
    robot.drive_to(start.x + 1.0, start.y)     # aim, drive a metre at most, re-aim, repeat
    robot.turn_to(90)                          # face +y
    frames = robot.look_around(45, "frames")   # 8 frames, each with its heading
    robot.checkpoint("look", frames=frames)    # one JSON line for you to judge
```

- `move(velocity, omega, duration_ms)` sends one move and returns `MoveReply(pose, distance, turned)`. A failure raises its typed error: `NotCarriedOut` (with `.pose`, read after the stall), `SessionNotActive`, `Busy`, `HoldProblem`, `CameraUnavailable`.
- `turn_by(degrees)` and `turn_to(heading)` turn in whole seconds with a scaled omega (73 degrees is omega 0.637 for 2000 ms) and correct until within 5 degrees.
- `forward(metres)`, `drive_to(x, y, tolerance=0.10, leg=1.0)`: `drive_to` re-aims from the measured pose every leg, raises `NotCarriedOut` on a stall and `NotArrived` when it runs out of moves.
- `pose()` reads from the robot; `where()` is the newest pose a reply gave. `trail` is every pose seen.
- `snapshot(path)`, `look_around(step_degrees, directory)`, `checkpoint(name, **data)`.
- `rooms()` is the rooms of the robot's map (`id`, `name`, `shape`), chosen by the pose's frame; `scene(places)` adds the trail and the robot for `render_map.py`. Neither needs a hold.
- When `PORTOS_LEASE` is already set, `Robot` uses that hold and leaves it open; whoever opens a hold releases it. So you can hold once, run several tricks under the same lease, judge their checkpoints between them, and release at the end.

## Tricks

A trick is `~/robot/tricks/<name>/run.py` plus `trick.yaml`:

```yaml
name: back-and-forth
does: Drives between two points N times, re-aiming from the measured pose each leg.
run: python3 tricks/back-and-forth/run.py <robot> --laps 3 --distance 1.0
parameters: {laps: round trips, distance: metres to B}
checkpoints: none; the last output line is the result
validated: 2026-11-12      # set after two good runs
runs: 2
successes: 2
notes: ["east legs run 3 percent short in the Laundry"]
```

A trick prints one JSON line per step and a last line with the result (`{"trick": ..., "ok": ..., ...}`), exits 0 only when it did what it meant, and releases in `finally` (the `with` block). Tricks call other tricks; keep them short and let checkpoints hand the judging back to you.

## House notes

Keep them in `~/robot/house/` (change the layout if a better one serves you):

- `places.yaml`: name, `x`, `y`, `heading`, the room group id, the map frame (`relativeToGroupRouteId` from the pose), reference photos, approach notes.
- `routes.yaml`: from, to, the trick or legs that get there, successes, last result.
- `calibration.yaml` and `traps.yaml`: what the house taught.
- `geometry/`: room shapes per map frame, for `render_map.py`.

Every place belongs to a map frame. Before using one, check that the robot's pose has the same `relativeToGroupRouteId`. A different frame means the map was rebuilt: the old places are wrong until you re-teach them.

## Maps

`render_map.py` draws rooms, holes, a metre grid, the robot and its heading, the trail and your places to a PNG, with only the standard library:

```python
robot = Robot("<robot endpoint id>")              # no hold needed to draw
scene = robot.scene(places=[{"name": "Sofa", "x": 3.0, "y": 1.5}])
json.dump(scene, open("scene.json", "w"))
```

```bash
python3 ~/robot/tricks/lib/render_map.py scene.json map.png --around 3.2,1.5 --radius 3
```

`scene.json` holds `rooms` (`name` and the GeoJSON `shape` from each room's `spatial/geometry` attribute), `trail` (`[[x, y], ...]` from `robot.trail`), `robot` (`x`, `y`, `heading`) and `places`. A room's shape lives on its group routes; only the route whose `relativeToGroupRouteId` matches the pose's is in the robot's map. Shapes change only when the robot remaps, so keep a copy in `~/robot/house/geometry/`. Look at the picture when you plan a route or explain where the robot is.

## When things go wrong

- **The hold you opened is gone** (`port session status` does not list it, or says `host-paused`): the session host stopped or your machine paused. Read the robot's state; if it is not docked, send it home with `port message robotic-vacuum-cleaner/return-to-dock <robot>`. Hold again before the next move.
- **A move keeps coming back not carried out:** something blocks that way. Look (`snapshot`), back off (`forward(-0.2)`), turn and go around. Record the trap.
- **The pose looks wrong** (the robot was picked up, or the map changed): take a frame of a known view such as the dock, compare it with your reference photo, and re-teach places if the frame changed.
- **A kept route fails** (a closed door, a moved chair): only the camera says why. Record the failure on the card and teach an alternative.
- **The camera refuses** (exit 14): another viewer may hold it (the app, a live view). Carry on without frames or try again shortly.
