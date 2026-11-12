"""Look at: point the camera at a pan and tilt angle and save a frame, then hand back.

    python3 tricks/look-at/run.py <camera> --pan 170 --tilt 40 --out sofa.jpg

Pan counts degrees right of the left end and tilt degrees up from the bottom end. Prints one
checkpoint line, {"checkpoint": "look-at", "frame": path, "position": ...}. The hold is released
only if this script opened it (PORTOS_LEASE unset); under your own hold the angles stay known for
the next trick in the same camera session.
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
    parser.add_argument("--pan", type=float, default=None, help="degrees right of the left end")
    parser.add_argument("--tilt", type=float, default=None, help="degrees up from the bottom end")
    parser.add_argument("--out", default="look-at.jpg", help="path for the frame")
    parser.add_argument("--cli", default="port", help="the port binary")
    parser.add_argument("--config-dir", default="", help="passed to port as --config-dir")
    args = parser.parse_args(argv)

    try:
        with Camera(args.camera, Port(args.cli, args.config_dir)) as camera:
            camera.look_at(args.pan, args.tilt)
            camera.checkpoint("look-at", frame=camera.snapshot(args.out))
    except CameraError as failure:
        print(json.dumps({"trick": "look-at", "ok": False, "error": str(failure)}), flush=True)
        return 1
    print(json.dumps({"trick": "look-at", "ok": True}), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
