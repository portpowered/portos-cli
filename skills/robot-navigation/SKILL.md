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
| `port session hold <robot> --motion --camera --json` | The same, and keeps the camera open: snapshots under the lease take about a second instead of a new camera connection each time. Use it when a trick looks often |
| `port session status --json` | The holds, their state, the camera's state, and why an ended hold ended |
| `port session release <holdId>` | Ends the session; the robot returns to its dock |
| `port message remote-control/move <robot> --wait --body '{"velocity":0.2,"omega":0,"duration":5000}' --json` | One move. The reply body has `pose`, `distanceMetres` and `turnedDegrees` |
| `port message move <robot> "rotate 90, forward 1m"` | The same moves from a short grammar, for one-off use |
| `port message go-to-point <robot> --frame <routeId> -- <x> <y> --json` | Sends the robot to a point in metres by its own path planning (it does not clean), waits for it to arrive, and prints the arrival pose. The `--` lets a coordinate be negative. Needs the lease: the host ends your session, sends the robot, and starts the session again. `--frame` is optional under a hold |
| `port endpoint attribute <robot> spatial/pose --force` | Reads the pose from the robot |
| `port message snapshot <robot> --output frame.jpg` | Saves the camera's current frame (from the held camera when your hold has one) |
| `port message record <robot> --output clip.mp4 --duration 30 --json` | Records the camera, with sound when it opens its own session. Under a `--camera` hold it records the held camera instead, at most 600 seconds, and it needs the lease; the clip has sound only when the hold was opened with `--listen`. `--audio-only --output room.wav` writes just the sound |
| `port message play-audio <robot> --file hi.ogg --json` | Plays a clip of up to 60 seconds on the robot's speaker and returns once it starts; `seconds` says how long it plays. Under a `--camera` hold it goes through the held camera (other formats are converted to Ogg/Opus with ffmpeg). A second clip while one plays exits 12. `--text` speaks text through a speech endpoint, `--chirp` plays a short tone, and under a `--listen` hold `--listen 10 --output reply.wav` records what the camera hears until 10 s after the clip. Before 09:00 local time it refuses unless `--allow-quiet-hours` |
| `port group list --expand interfaces.attributes --json` | Your groups; each room's `spatial/geometry` attribute holds its shape and map frame. Follow `next_token` for more pages |

A move: `velocity` in m/s (negative reverses), `omega` in rad/s (positive turns left), `duration` in whole seconds as milliseconds (1000 to 5000 on the S8). Distance is velocity times duration; the turn is omega times duration. The robot rejects a drive under 5 cm and a turn in place under 5 degrees. The pose is metres in the map's frame with heading 0 along +x, growing counter-clockwise.

The session host sends a zero-motion keepalive while you think, so the robot stays under remote control between your commands. The host owns `seqNum`; send any value.

Exit codes let a script branch without reading text:

| Exit | Means | Do |
| --- | --- | --- |
| 0 | done | read the measured reply |
| 10 | the move was not carried out (obstacle, wall, dock), or a go-to-point trip reported an error or took over 180 seconds (the robot was told to stop) | read the pose, change direction or go around |
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
- `turn_by(degrees)` and `turn_to(heading)` turn in whole seconds with a scaled omega (73 degrees is omega 0.637 for 2000 ms) and correct until within 5 degrees. The robot refuses a turn that measures under 5 degrees, and it turns a little short, so a correction under 10 degrees that comes back refused counts as close enough.
- `forward(metres)`, `drive_to(x, y, tolerance=0.10, leg=1.0)`: `drive_to` re-aims from the measured pose before a leg when driving on would miss the point by more than half the tolerance, raises `NotCarriedOut` on a stall and `NotArrived` when it runs out of moves.
- `go_to(x, y)` sends the robot across the house by its own route and returns the arrival pose; see Crossing rooms below.
- `pose()` reads from the robot; `where()` is the newest pose a reply gave. `trail` is every pose seen.
- `snapshot(path)`, `look_around(step_degrees, directory)`, `checkpoint(name, **data)`.
- `rooms()` is the rooms of the robot's map (`id`, `name`, `shape`), chosen by the pose's frame; `scene(places)` adds the trail and the robot for `render_map.py`. Neither needs a hold.
- `Robot(endpoint, camera=True)` holds the camera too (`--camera`), so each snapshot takes about a second.
- When `PORTOS_LEASE` is already set, `Robot` uses that hold and leaves it open; whoever opens a hold releases it. So you can hold once, run several tricks under the same lease, judge their checkpoints between them, and release at the end.

## Crossing rooms

`go_to(x, y)` is the way to cross a room or the house: the robot plans its own route around furniture, so it needs no chain of legs. One call ends your session, sends the robot, waits for it to stop (up to 180 seconds), starts the session again and gives you the arrival pose. It does not clean. The point is in the pose's map frame, in metres; a point off the map is refused, and so is a frame that is not the pose's.

The robot stops a few centimetres short of the point (about 5 cm on the S8), faces the way it travelled, and may sit in a spot you cannot see. So use each for what it is good at:

- `go_to` for distance: the far side of a room, another room, the sofa from the dock. It knows the walls, so it is the safe choice when you cannot see the way.
- `drive_to` and `turn_to` for the last metre: placing the robot on a mark, squaring up to a wall or lining up the camera. They are measured, one leg at a time, from the pose in each reply.

Finish a crossing with a check: `robot.go_to(x, y)`, then `robot.drive_to(x, y, tolerance=0.05)` if it matters, then `robot.turn_to(heading)` and a snapshot. A move sent while a trip runs waits until the trip finishes, so run one script at a time. `go_to` raises `NotCarriedOut` when the robot reports an error or the trip times out (read `.pose`, look, try another route), and `RobotError` for a refusal such as a point outside the map.

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

`scene.json` holds `rooms` (`name` and the GeoJSON `shape` from each room's `spatial/geometry` attribute), `trail` (`[[x, y], ...]` from `robot.trail`), `robot` (`x`, `y`, `heading`) and `places`. A room's shape lives on its group routes; only the route whose `relativeToGroupRouteId` matches the pose's is in the robot's map. Shapes change only when the robot remaps, so keep a copy in `~/robot/house/geometry/`. Look at the picture when you plan a route or explain where the robot is. Choose every point a trick drives to from the shapes: inside a room, clear of its edges and holes by at least 0.3 m, with the straight path to it inside the room too. Holes are furniture. A point given only as a distance ahead (`back-and-forth --distance`) can land in one, and the robot then stops short with exit code 10.

## When things go wrong

- **The hold you opened is gone** (`port session status` does not list it, or says `host-paused`): the session host stopped or your machine paused. Read the robot's state; if it is not docked, send it home with `port message robotic-vacuum-cleaner/return-to-dock <robot>`. Hold again before the next move.
- **The release or the hold reports a provider timeout** (on start-session, end-session or return-to-dock): the host already sent the robot home up to three times, so the answer can take two minutes. Read the robot's state; if it is still not docked, wait a minute and send it home with `port message robotic-vacuum-cleaner/return-to-dock <robot>`. Do not start the next run until it is docked.
- **A move keeps coming back not carried out:** something blocks that way. Look (`snapshot`) before you release, so the stall comes with a frame; back off (`forward(-0.2)`), turn and go around. When the map shows the way open, the block is something the map does not know (a bag, a box): let `go_to` plan around it for that leg. Record the trap.
- **The pose looks wrong** (the robot was picked up, or the map changed): take a frame of a known view such as the dock, compare it with your reference photo, and re-teach places if the frame changed.
- **A kept route fails** (a closed door, a moved chair): only the camera says why. Record the failure on the card and teach an alternative.
- **The camera refuses** (exit 14): another viewer may hold it (the app, a live view). Under a `--camera` hold this means the held camera was taken away; `port session status` shows `lostReason`. The robot stays held; carry on without frames, or release and hold again.
