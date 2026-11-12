#!/bin/sh
# opkg wrapper: refuse package operations that must write to the rootfs when /
# is mounted read-only (e.g. release images). Installing/removing on a read-only
# rootfs fails part-way and leaves a corrupt opkg state, so block it up front.
# Read-only queries (list, list-installed, info, status, files, search, ...)
# are passed straight through.

REAL=/usr/bin/opkg.real

# A package operation is "mutating" if any argument is one of these verbs. We
# scan all args (rather than positionally) so option ordering can't fool us; the
# only cost is a rare false match if a package/option value is literally one of
# these words, which is acceptable for a safety guard.
mutating=0
for arg in "$@"; do
	case "$arg" in
	install | remove | upgrade | configure | flag | update | download)
		mutating=1
		break
		;;
	esac
done

if [ "$mutating" = "1" ]; then
	# Mount options for / from /proc/mounts (last matching entry wins). The
	# options field is comma-separated and begins with ro or rw.
	root_opts=$(awk '$2 == "/" { o = $4 } END { print o }' /proc/mounts)
	case ",$root_opts," in
	*,ro,*)
		echo "opkg: / is mounted read-only; refusing this operation to avoid a corrupt package state." >&2
		echo "opkg: remount read-write first if you really intend to modify the rootfs (mount -o remount,rw /)." >&2
		exit 1
		;;
	esac
fi

exec "$REAL" "$@"
