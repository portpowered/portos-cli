"""Patrol: check left, the middle and right, saving a frame at each.

    python3 tricks/patrol/run.py <camera> --out patrol/ [--tilt 60]

Prints one checkpoint line, {"checkpoint": "patrol", "frames": [{name, path, pan, tilt}], ...},
for you to look at each frame. The camera stays at its right end; the hold is released only if
this script opened it (PORTOS_LEASE unset).
"""

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "lib"))

from portos_camera import Camera, CameraError, Port  # noqa: E402


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("camera")
    parser.add_argument("--tilt", type=float, default=None, help="tilt up from the bottom end to patrol at")
    parser.add_argument("--out", default="patrol", help="directory for the frames")
    parser.add_argument("--cli", default="port", help="the port binary")
    parser.add_argument("--config-dir", default="", help="passed to port as --config-dir")
    args = parser.parse_args(argv)

    try:
        with Camera(args.camera, Port(args.cli, args.config_dir)) as camera:
            frames = camera.patrol(args.out, args.tilt)
            camera.checkpoint("patrol", frames=frames)
    except CameraError as failure:
        print(json.dumps({"trick": "patrol", "ok": False, "error": str(failure)}), flush=True)
        return 1
    print(json.dumps({"trick": "patrol", "ok": True}), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
