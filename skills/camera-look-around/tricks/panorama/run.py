"""Panorama: sweep the camera's full pan range, saving a frame every few degrees, then hand back.

    python3 tricks/panorama/run.py <camera> --every 60 --out frames/

Prints one checkpoint line, {"checkpoint": "panorama", "frames": [{path, pan, tilt}], "position": ...},
for you to look at the frames. The camera stays at its right end; the hold is released only if this
script opened it (PORTOS_LEASE unset).
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
    parser.add_argument("--every", type=float, default=60.0, help="most degrees between two frames")
    parser.add_argument("--tilt", type=float, default=None, help="tilt up from the bottom end to sweep at")
    parser.add_argument("--out", default="panorama", help="directory for the frames")
    parser.add_argument("--cli", default="port", help="the port binary")
    parser.add_argument("--config-dir", default="", help="passed to port as --config-dir")
    args = parser.parse_args(argv)

    try:
        with Camera(args.camera, Port(args.cli, args.config_dir)) as camera:
            frames = camera.panorama(args.out, args.every, args.tilt)
            camera.checkpoint("panorama", frames=frames)
    except CameraError as failure:
        print(json.dumps({"trick": "panorama", "ok": False, "error": str(failure)}), flush=True)
        return 1
    print(json.dumps({"trick": "panorama", "ok": True}), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
