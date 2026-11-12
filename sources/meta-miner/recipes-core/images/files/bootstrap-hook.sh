#!/bin/sh
# Bootstrap FIP installation on much older systems that run native SWUpdate
# directly and lack a suitable proto-swupdate. This is a legacy migration hook,
# not a replacement for the normal updater.
set -eu

# Modern proto-swupdate suppresses every native child because it owns the FIP pass.
# This bootstrap hook is only for legacy callers.
[ "${PROTO_SWUPDATE_SKIP_FIP:-}" != 1 ] || exit 0

fail() {
    echo "FIP bootstrap: $*" >&2
    exit 1
}

skip_bootstrap() {
    echo "FIP bootstrap: $*; skipping bootstrap FIP installation" >&2
    exit 0
}

umask 077
stage=$(mktemp -d /tmp/.proto-swupdate-bootstrap.XXXXXX)
trap 'rm -rf "$stage"' 0
trap 'exit 1' 1 2 3 15
mkdir "$stage/tmp"
cp "${TMPDIR:-/tmp}/sw-description" "$stage/tmp/trusted-description"
[ -s "$stage/tmp/trusted-description" ] || fail "missing active manifest"

# Arguments, not line splitting: archive paths may contain spaces or newlines.
# Keep only the source pathname. A second match skips bootstrap; never choose an ambiguous archive.
find /data /tmp -type f -name '*.swu' -exec /bin/sh -ec '
    stage=$1
    shift
    for archive do
        if cpio -i --to-stdout sw-description < "$archive" > "$stage/tmp/description" 2>/dev/null &&
           cmp -s "$stage/tmp/trusted-description" "$stage/tmp/description"; then
            if [ -L "$stage/archive.swu" ]; then
                echo "multiple archives match active manifest" > "$stage/error"
            else
                ln -s "$archive" "$stage/archive.swu" || echo "archive selection failed" > "$stage/error"
            fi
        fi
    done
' bootstrap-discovery "$stage" {} + || skip_bootstrap "archive discovery failed"
[ ! -e "$stage/error" ] || skip_bootstrap "$(cat "$stage/error")"
[ -L "$stage/archive.swu" ] || skip_bootstrap "no archive matches active manifest"

# Pin the source inode read-only, without copying SWU bytes into tmpfs.
# Native SWUpdate uses fixed /tmp sockets; isolate its workspace from the parent.
/usr/bin/unshare --mount --propagation private /bin/sh -ec '
    stage=$1
    : > "$stage/tmp/archive.swu"
    /bin/mount --bind "$stage/archive.swu" "$stage/tmp/archive.swu"
    /bin/mount -o remount,bind,ro "$stage/tmp/archive.swu"
    /bin/mount --rbind "$stage/tmp" /tmp
    cpio -i --to-stdout sw-description < /tmp/archive.swu > /tmp/description
    cmp -s /tmp/trusted-description /tmp/description || { echo "FIP bootstrap: archive manifest changed" >&2; exit 1; }
    cpio -i --to-stdout proto-swupdate < /tmp/archive.swu > /tmp/proto-swupdate
    actual=$(sha256sum < /tmp/proto-swupdate)
    actual=${actual%% *}
    [ "$actual" = "@UPDATER_SHA256@" ] || { echo "FIP bootstrap: embedded updater hash mismatch" >&2; exit 1; }
    chmod 0755 /tmp/proto-swupdate
    rm /tmp/trusted-description /tmp/description
    export TMPDIR=/tmp RUNTIME_DIRECTORY=/tmp
    unset NOTIFY_SOCKET LISTEN_PID LISTEN_FDS LISTEN_FDNAMES
    exec /tmp/proto-swupdate install --fip /tmp/archive.swu
' bootstrap-install "$stage"
