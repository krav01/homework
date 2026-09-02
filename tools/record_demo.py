#!/usr/bin/env python3
"""Capture real command output as asciicast v2 and a self-contained replay page."""

import argparse
import datetime
import html
import json
import os
from pathlib import Path
import subprocess
import sys
import time


def replay_page(header, events, exit_code):
    # Escape '<' inside JSON so even untrusted output cannot end the script tag.
    payload = json.dumps(events, ensure_ascii=True).replace("<", "\\u003c")
    provenance = html.escape(header["title"])
    status = "PASSED" if exit_code == 0 else f"FAILED (exit {exit_code})"
    return """<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>Sunday System — recorded demo</title>
<style>
body{margin:0;background:#10151f;color:#e6edf3;font:16px system-ui;padding:28px}
main{max-width:1100px;margin:auto}h1{font-size:26px}p{color:#b2bdd0;overflow-wrap:anywhere}
button,select{font:inherit;padding:8px 14px;margin:0 8px 12px 0;background:#253349;color:white;border:1px solid #50627e;border-radius:6px}
pre{height:60vh;overflow:auto;background:#080c13;padding:22px;border-radius:10px;white-space:pre-wrap;overflow-wrap:anywhere;font:15px/1.6 monospace}
</style><main><h1>Sunday System: recovery and persistence</h1>
<p>Recorded from a real Kubernetes acceptance test. No simulated command output.</p>
<p>PROVENANCE</p><p>Result: STATUS</p>
<button id="play">Play</button><button id="restart">Restart</button>
<label>Speed <select id="speed"><option value="1">1×</option><option value="2" selected>2×</option><option value="4">4×</option></select></label>
<pre id="terminal" aria-label="Recorded terminal output"></pre>
<script id="events" type="application/json">PAYLOAD</script>
<script>
const events=JSON.parse(document.getElementById('events').textContent);
const terminal=document.getElementById('terminal'), play=document.getElementById('play');
let playing=false, elapsed=0, index=0, last=performance.now();
play.onclick=()=>{playing=!playing;play.textContent=playing?'Pause':'Play';};
document.getElementById('restart').onclick=()=>{elapsed=0;index=0;terminal.textContent='';playing=true;play.textContent='Pause';};
function frame(now){
 if(playing){elapsed+=(now-last)/1000*Number(document.getElementById('speed').value);
  while(index<events.length&&events[index][0]<=elapsed){terminal.textContent+=events[index++][2];terminal.scrollTop=terminal.scrollHeight;}
  if(index===events.length){playing=false;play.textContent='Finished';}
 }
 last=now;requestAnimationFrame(frame);
}
requestAnimationFrame(frame);
</script></main></html>""".replace("PROVENANCE", provenance).replace("STATUS", status).replace("PAYLOAD", payload)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command:
        parser.error("a command is required after --")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    recorded_at = datetime.datetime.now(datetime.timezone.utc).isoformat()
    commit = os.environ.get("GITHUB_SHA", "local")
    header = {"version": 2, "width": 110, "height": 32, "timestamp": int(time.time()),
              "title": f"Sunday System | {recorded_at} | commit {commit}",
              "env": {"TERM": "xterm-256color"}}
    events = []
    started = time.monotonic()
    # No shell interpolation: the caller supplies an explicit executable and arguments.
    with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                          text=True, encoding="utf-8", errors="replace", bufsize=1) as process:
        for line in process.stdout:
            sys.stdout.write(line)
            sys.stdout.flush()
            events.append([round(time.monotonic() - started, 3), "o", line.replace("\n", "\r\n")])
        exit_code = process.wait()
    events.append([round(time.monotonic() - started, 3), "o", f"\r\nExit status: {exit_code}\r\n"])
    cast = "\n".join(json.dumps(item) for item in [header, *events]) + "\n"
    args.output.with_suffix(".cast").write_text(cast, encoding="utf-8")
    args.output.with_suffix(".html").write_text(replay_page(header, events, exit_code), encoding="utf-8")
    args.output.with_suffix(".txt").write_text("".join(event[2] for event in events), encoding="utf-8")
    print("::group::SUNDAY_DEMO_CAST")
    print(cast, end="")
    print("::endgroup::")
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
