#!/bin/sh
# Apply PUID/PGID if set, otherwise keep defaults (911/911).
PUID=${PUID:-911}
PGID=${PGID:-911}

groupmod -o -g "$PGID" drumdrop 2>/dev/null || true
usermod -o -u "$PUID" drumdrop 2>/dev/null || true

# Guarded chown: only sweep a dir when its top-level owner doesn't already match
# PUID:PGID (downloads can be huge — never blind-chown -R every boot). Fail-safe:
# if `stat` is unavailable the id mismatches and the recurse runs (same as before).
for dir in /config /downloads; do
  cur_uid=$(stat -c '%u' "$dir" 2>/dev/null || echo -1)
  cur_gid=$(stat -c '%g' "$dir" 2>/dev/null || echo -1)
  if [ "$cur_uid" != "$PUID" ] || [ "$cur_gid" != "$PGID" ]; then
    chown -R drumdrop:drumdrop "$dir"
  fi
done

exec gosu drumdrop "$@"
