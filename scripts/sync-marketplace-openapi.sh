#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <marketplace-openapi.json>" >&2
  exit 2
fi

source_file=$1
repo_root=$(cd "$(dirname "$0")/.." && pwd)
target_file="$repo_root/internal/registry/specs/marketplace.json"
tmp_file=$(mktemp)
trap 'rm -f "$tmp_file"' EXIT

jq '
  if .openapi == null then error("missing openapi version") else . end
  | if .paths["/skills/{skill_id}"].get.operationId != "skill.get"
      then error("missing skill.get") else . end
  | if .paths["/skills/{skill_id}/download"].get.operationId != "skill.download"
      then error("missing skill.download") else . end
  | {
      openapi,
      info,
      servers,
      "x-octo-service": "marketplace",
      "x-octo-base-url": "OCTO_MARKETPLACE_API_PREFIX",
      "x-octo-space-header": true,
      "x-octo-manual-command": true,
      paths: {
        "/skills/{skill_id}": {
          get: .paths["/skills/{skill_id}"].get
        },
        "/skills/{skill_id}/download": {
          get: .paths["/skills/{skill_id}/download"].get
        }
      },
      components
    }
' "$source_file" > "$tmp_file"

mv "$tmp_file" "$target_file"
trap - EXIT
echo "updated $target_file"
