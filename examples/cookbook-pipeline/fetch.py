"""Fetch the immutable cookbook implementation and verify its digest."""
import hashlib
import json
from pathlib import Path
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parent

def fetch():
    pin = json.loads((ROOT / "cookbook.json").read_text())
    url = f"https://raw.githubusercontent.com/{pin['repository']}/{pin['commit']}/{pin['path']}"
    target = ROOT / ".runtime" / "flight.py"
    if not target.exists():
        with urlopen(url, timeout=30) as response:
            content = response.read()
    else:
        content = target.read_bytes()
    if hashlib.sha256(content).hexdigest() != pin["sha256"]:
        raise ValueError("Cookbook source does not match the reviewed digest")
    target.parent.mkdir(exist_ok=True)
    target.write_bytes(content)
    return target

if __name__ == "__main__":
    print(fetch())
