"""Download the published OpenAPI document into spec/openapi.json."""
import json
import os
import urllib.request
from pathlib import Path

url = os.environ.get("SMTPFAST_SPEC_URL", "https://smtpfa.st/api/v1/openapi.json")
# Cloudflare refuses the default Python-urllib user agent (error 1010).
request = urllib.request.Request(url, headers={"User-Agent": "smtpfast-go-spec-sync"})
with urllib.request.urlopen(request, timeout=30) as res:
    spec = json.load(res)
out = Path(__file__).resolve().parent.parent / "spec/openapi.json"
out.write_text(json.dumps(spec, indent=2) + "\n")
print(f"spec/openapi.json: {len(spec['paths'])} paths from {url}")
