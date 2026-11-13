"""Gesture: play a short gesture with the camera head, such as a nod, and end where it started.

    python3 tricks/gesture/run.py <camera> nod [--times 2]

Gestures: nod (yes), quick-nod (mm-hm), big-nod (an eager YES), shake (no), jiggle (a happy
wiggle), look-up-down (curious). Prints one
checkpoint line, {"checkpoint": "gesture", "gesture": ..., "position": ...}. The hold is released
only if this script opened it (PORTOS_LEASE unset).
"""

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "lib"))

from portos_camera import GESTURES, Camera, CameraError, Port  # noqa: E402


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("camera")
    parser.add_argument("gesture", choices=sorted(GESTURES))
    parser.add_argument("--times", type=int, default=1, help="how many times to play it")
    parser.add_argument("--cli", default="port", help="the port binary")
    parser.add_argument("--config-dir", default="", help="passed to port as --config-dir")
    args = parser.parse_args(argv)

    try:
        with Camera(args.camera, Port(args.cli, args.config_dir)) as camera:
            for _ in range(max(args.times, 1)):
                camera.gesture(args.gesture)
            camera.checkpoint("gesture", gesture=args.gesture)
    except CameraError as failure:
        print(json.dumps({"trick": "gesture", "ok": False, "error": str(failure)}), flush=True)
        return 1
    print(json.dumps({"trick": "gesture", "ok": True}), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
