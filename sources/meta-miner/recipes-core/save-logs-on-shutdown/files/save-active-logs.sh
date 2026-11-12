#!/bin/bash
# Save nonempty /var/log/<directory>/*.log files on orderly shutdown.
# Match logrotate's archive naming without depending on application names.

SOURCE_ROOT="${SOURCE_LOG_ROOT:-/var/log}"
DESTINATION_ROOT="${PERSISTENT_LOG_ROOT:-/data/log}"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
REQUIRED_KB=51200
# Bound shutdown archives even for applications without flash logrotate rules.
# Application logrotate rules may enforce a smaller limit.
MAX_ARCHIVES=30
shopt -s nullglob

# Clean up an interrupted copy if systemd reaches TimeoutStopSec.
trap '[ -z "${temporary:-}" ] || rm -f -- "$temporary"' EXIT
trap 'exit 0' TERM INT

# Match existing logrotate cleanup: keep newest archives by modification time.
# Arrays preserve spaces in paths.
prune_archives() {
    local directory="$1" archive position swap
    local -a retained=()
    for archive in "$directory"/*.log-*.gz; do
        [ -f "$archive" ] && [ ! -L "$archive" ] || continue
        position=${#retained[@]}
        retained+=("$archive")
        while (( position > 0 )) && [[ "${retained[position]}" -nt "${retained[position-1]}" ]]; do
            swap=${retained[position-1]}
            retained[position-1]=${retained[position]}
            retained[position]=$swap
            position=$((position - 1))
        done
        if (( ${#retained[@]} > MAX_ARCHIVES )); then
            rm -f -- "${retained[MAX_ARCHIVES]}" || echo "WARNING: Could not prune ${retained[MAX_ARCHIVES]}" >&2
            unset 'retained[MAX_ARCHIVES]'
        fi
    done
}

if [ -L "$DESTINATION_ROOT" ] || ! mkdir -p "$DESTINATION_ROOT"; then
    echo "WARNING: Persistent log directory unavailable. Skipping log save." >&2
    exit 0
fi

for directory in "$SOURCE_ROOT"/*; do
    [ -d "$directory" ] && [ ! -L "$directory" ] || continue
    destination="$DESTINATION_ROOT/${directory##*/}"
    for source in "$directory"/*.log; do
        [ -f "$source" ] && [ ! -L "$source" ] && [ -s "$source" ] || continue

        # Recheck for each file; leave a reserve and room for incompressible data.
        available_kb=$(df -Pk "$DESTINATION_ROOT" | awk 'NR==2 {print $4}')
        source_bytes=$(wc -c < "$source")
        if ! [[ "$available_kb" =~ ^[0-9]+$ && "$source_bytes" =~ ^[[:space:]]*[0-9]+[[:space:]]*$ ]]; then
            echo "WARNING: Could not determine space for $source. Skipping." >&2
            continue
        fi
        required_kb=$((REQUIRED_KB + source_bytes / 1024 + source_bytes / 102400 + 64))
        if (( available_kb < required_kb )); then
            echo "WARNING: Insufficient space for $source (${available_kb}KB available, ${required_kb}KB required). Skipping." >&2
            continue
        fi
        if [ -L "$destination" ] || ! mkdir -p "$destination"; then
            echo "WARNING: Could not create $destination. Skipping $source." >&2
            continue
        fi

        archive="$destination/${source##*/}-${TIMESTAMP}.gz"
        temporary=$(mktemp "$destination/.shutdown-log.XXXXXX") || continue
        # Never expose a partial gzip as an archive. One failure must not skip
        # later services; nginx, for example, may still be writing during copy.
        if gzip -c -- "$source" > "$temporary" && mv -f -- "$temporary" "$archive"; then
            echo "Saved $source to $archive"
        else
            echo "WARNING: Could not save $source" >&2
        fi
        rm -f -- "$temporary"
    done
    [ ! -L "$destination" ] && [ -d "$destination" ] && prune_archives "$destination"
done

exit 0
