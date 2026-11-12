#!/bin/sh
# Rewrites --tfw-nvctr / --ntfw-nvctr values from the caller to those supplied
# via env vars TFW_NVCTR_VAL / NTFW_NVCTR_VAL, then execs the real cert_create.
# CERT_CREATE_REAL must point at the actual cert_create binary.
set -eu
: "${CERT_CREATE_REAL:?CERT_CREATE_REAL must be set}"
: "${TFW_NVCTR_VAL:?TFW_NVCTR_VAL must be set}"
: "${NTFW_NVCTR_VAL:?NTFW_NVCTR_VAL must be set}"

# Build a new positional arg list with the nvctr values replaced. Sentinel
# "--" separates already-processed args from the original tail.
set -- "$@" --
while [ "$1" != -- ]; do
    case "$1" in
        --tfw-nvctr)   shift 2; set -- "$@" --tfw-nvctr "$TFW_NVCTR_VAL"  ;;
        --ntfw-nvctr)  shift 2; set -- "$@" --ntfw-nvctr "$NTFW_NVCTR_VAL" ;;
        *)             arg=$1; shift; set -- "$@" "$arg"                   ;;
    esac
done
shift  # drop sentinel
exec "$CERT_CREATE_REAL" "$@"
