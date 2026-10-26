#!/bin/bash
#
# Integration test script for go_fastboot
# Tests against a real miner device to verify all changes work correctly.
#
# Usage:
#   ./scripts/integration_test.sh <device_ip> [--full] [--skip-build] [--verbose] [--flash <path>]
#
# Options:
#   --full          Run full test including enter/exit fastboot (takes ~5 min)
#                   Without this flag, only runs non-disruptive tests (~30 sec)
#   --skip-build    Skip building and use go_fastboot from PATH or current directory
#   --verbose       Show go_fastboot output during tests
#   --flash <path>  Flash firmware from zip archive or single image file (requires --full)
#                   - Zip files test flashing ALL partitions with auto-reboot
#                   - Single files test flashing SPECIFIC partition without auto-reboot
#
# Prerequisites:
#   - Device must be reachable on the network
#   - Device should be in Linux mode to start
#   - For --full tests: device will reboot multiple times
#

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test counters
TESTS_RUN=0
TESTS_PASSED=0
TESTS_FAILED=0

# Configuration
TIMEOUT_CMD=30      # Timeout for simple commands (seconds)
TIMEOUT_ENTER=180   # Timeout for enter fastboot (seconds)
TIMEOUT_EXIT=120    # Timeout for exit fastboot (seconds)
TIMEOUT_FLASH=600   # Timeout for flash operation (seconds)

usage() {
    echo "Usage: $0 <device_ip> [--full] [--skip-build] [--verbose] [--flash <path>]"
    echo ""
    echo "Options:"
    echo "  --full          Run full test including enter/exit fastboot"
    echo "                  Without this flag, only runs non-disruptive tests"
    echo "  --skip-build    Skip building and use go_fastboot from PATH or current directory"
    echo "  --verbose       Show go_fastboot output during tests"
    echo "  --flash <path>  Flash firmware from zip (all partitions) or single image (requires --full)"
    echo ""
    echo "Example:"
    echo "  $0 192.168.1.100                                  # Quick tests only"
    echo "  $0 192.168.1.100 --full                           # Full test suite"
    echo "  $0 192.168.1.100 --skip-build                     # Use system go_fastboot"
    echo "  $0 192.168.1.100 --verbose                        # Show command output"
    echo "  $0 192.168.1.100 --full --flash ./firmware.zip   # Test all partitions flash (auto-reboot)"
    echo "  $0 192.168.1.100 --full --flash ./rootfs.ext4    # Test specific partition flash (no auto-reboot)"
    exit 1
}

log_info() {
    echo -e "${YELLOW}[INFO]${NC} $1"
}

log_pass() {
    echo -e "${GREEN}[PASS]${NC} $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

log_fail() {
    echo -e "${RED}[FAIL]${NC} $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
}

run_test() {
    local name="$1"
    local timeout="$2"
    shift 2
    local cmd=("$@")

    TESTS_RUN=$((TESTS_RUN + 1))
    log_info "Running: $name"

    # Run command with timeout
    local start_time=$(date +%s)
    local output
    local exit_code=0

    if [ "$VERBOSE" = true ]; then
        echo "  \$ ${cmd[*]}"
        timeout "${timeout}s" "${cmd[@]}" 2>&1 | tee /tmp/go_fastboot_test_output.$$ || exit_code=$?
        output=$(cat /tmp/go_fastboot_test_output.$$ 2>/dev/null || echo "")
        rm -f /tmp/go_fastboot_test_output.$$
    else
        output=$(timeout "${timeout}s" "${cmd[@]}" 2>&1) || exit_code=$?
    fi

    local end_time=$(date +%s)
    local duration=$((end_time - start_time))

    if [ $exit_code -eq 124 ]; then
        log_fail "$name - TIMEOUT after ${timeout}s (possible spinner deadlock)"
        echo "  Command: ${cmd[*]}"
        return 1
    elif [ $exit_code -ne 0 ]; then
        log_fail "$name - Exit code $exit_code (${duration}s)"
        echo "  Command: ${cmd[*]}"
        [ "$VERBOSE" = false ] && echo "  Output: $output"
        return 1
    else
        log_pass "$name (${duration}s)"
        return 0
    fi
}

run_test_expect_output() {
    local name="$1"
    local timeout="$2"
    local pattern="$3"
    shift 3
    local cmd=("$@")

    TESTS_RUN=$((TESTS_RUN + 1))
    log_info "Running: $name"

    local output
    local exit_code=0

    if [ "$VERBOSE" = true ]; then
        echo "  \$ ${cmd[*]}"
        timeout "${timeout}s" "${cmd[@]}" 2>&1 | tee /tmp/go_fastboot_test_output.$$ || exit_code=$?
        output=$(cat /tmp/go_fastboot_test_output.$$ 2>/dev/null || echo "")
        rm -f /tmp/go_fastboot_test_output.$$
    else
        output=$(timeout "${timeout}s" "${cmd[@]}" 2>&1) || exit_code=$?
    fi

    if [ $exit_code -eq 124 ]; then
        log_fail "$name - TIMEOUT after ${timeout}s"
        return 1
    elif [ $exit_code -ne 0 ]; then
        log_fail "$name - Exit code $exit_code"
        [ "$VERBOSE" = false ] && echo "  Output: $output"
        return 1
    elif ! echo "$output" | grep -q "$pattern"; then
        log_fail "$name - Expected pattern '$pattern' not found"
        [ "$VERBOSE" = false ] && echo "  Output: $output"
        return 1
    else
        log_pass "$name"
        return 0
    fi
}

# Parse arguments
if [ $# -lt 1 ]; then
    usage
fi

TARGET="$1"
FULL_TEST=false
SKIP_BUILD=false
VERBOSE=false
FLASH_PATH=""

shift
while [ $# -gt 0 ]; do
    case "$1" in
        --full)
            FULL_TEST=true
            ;;
        --skip-build)
            SKIP_BUILD=true
            ;;
        --verbose)
            VERBOSE=true
            ;;
        --flash)
            shift
            if [ $# -eq 0 ]; then
                echo "Error: --flash requires a path argument"
                usage
            fi
            FLASH_PATH="$1"
            ;;
        *)
            echo "Unknown option: $1"
            usage
            ;;
    esac
    shift
done

# Validate --flash requires --full
if [ -n "$FLASH_PATH" ] && [ "$FULL_TEST" = false ]; then
    echo "Error: --flash requires --full (flashing is a disruptive operation)"
    exit 1
fi

# Validate flash path exists
if [ -n "$FLASH_PATH" ] && [ ! -e "$FLASH_PATH" ]; then
    echo "Error: Flash path does not exist: $FLASH_PATH"
    exit 1
fi

# Determine which binary to use
if [ "$SKIP_BUILD" = true ]; then
    # Stay in current directory and look for go_fastboot
    if [ -x "./go_fastboot" ]; then
        GO_FASTBOOT="./go_fastboot"
        log_info "Using local binary: $GO_FASTBOOT"
    elif command -v go_fastboot &> /dev/null; then
        GO_FASTBOOT="go_fastboot"
        log_info "Using system binary: $(which go_fastboot)"
    else
        echo "Error: go_fastboot not found in PATH or current directory"
        exit 1
    fi
else
    # Change to the go_fastboot directory for building
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    cd "$SCRIPT_DIR/.."

    # Build first
    log_info "Building go_fastboot..."
    go build -o go_fastboot ./cmd/go_fastboot || {
        echo "Build failed!"
        exit 1
    }
    log_pass "Build successful"
    GO_FASTBOOT="./go_fastboot"
fi

echo ""
echo "============================================"
echo "Testing go_fastboot against: $TARGET"
echo "Full test mode: $FULL_TEST"
echo "Verbose: $VERBOSE"
echo "Binary: $GO_FASTBOOT"
[ -n "$FLASH_PATH" ] && echo "Flash path: $FLASH_PATH"
echo "============================================"
echo ""

#
# Quick Tests (non-disruptive)
#

log_info "=== Quick Tests (non-disruptive) ==="
echo ""

# Test 1: Version command
run_test "Version command" $TIMEOUT_CMD $GO_FASTBOOT --version

# Test 2: Help command
run_test "Help command" $TIMEOUT_CMD $GO_FASTBOOT --help

# Test 3: Target set
run_test "Target set" $TIMEOUT_CMD $GO_FASTBOOT target "$TARGET"

# Test 4: Target show
run_test "Target show" $TIMEOUT_CMD $GO_FASTBOOT target

# Test 5: Status command (tests spinner and SSH connection, includes version info)
run_test "Status command (spinner test)" $TIMEOUT_CMD $GO_FASTBOOT status

# Test 6: Multiple rapid status calls (tests spinner cleanup)
log_info "Running: Rapid status calls (spinner lifecycle test)"
TESTS_RUN=$((TESTS_RUN + 1))
rapid_status_success=true
for i in 1 2 3; do
    if [ "$VERBOSE" = true ]; then
        echo "  \$ $GO_FASTBOOT status (iteration $i)"
        if ! timeout ${TIMEOUT_CMD}s $GO_FASTBOOT status 2>&1; then
            log_fail "Rapid status calls - iteration $i failed"
            rapid_status_success=false
            break
        fi
    else
        if ! timeout ${TIMEOUT_CMD}s $GO_FASTBOOT status >/dev/null 2>&1; then
            log_fail "Rapid status calls - iteration $i failed"
            rapid_status_success=false
            break
        fi
    fi
done
if [ "$rapid_status_success" = true ]; then
    log_pass "Rapid status calls (3 iterations)"
fi

#
# Full Tests (disruptive - reboots device)
#

if [ "$FULL_TEST" = true ]; then
    echo ""
    log_info "=== Full Tests (device will reboot) ==="
    echo ""

    # Test: Enter fastboot mode
    run_test "Enter fastboot mode" $TIMEOUT_ENTER $GO_FASTBOOT enter

    # Give device a moment to stabilize
    sleep 2

    # Test: Status in fastboot mode
    run_test "Status in fastboot mode" $TIMEOUT_CMD $GO_FASTBOOT status

    # Test: Flash firmware (if path provided)
    if [ -n "$FLASH_PATH" ]; then
        echo ""
        log_info "=== Flash Tests ==="
        echo ""

        # Determine if this is a zip file (all partitions) or single file (specific partition)
        FLASH_IS_ZIP=false
        if [ -f "$FLASH_PATH" ] && [[ "$FLASH_PATH" == *.zip ]]; then
            FLASH_IS_ZIP=true
            log_info "Flash path is a zip file - will test flashing all partitions with auto-reboot"
        else
            log_info "Flash path is single image - testing specific partition flash without auto-reboot"
        fi

        if [ "$FLASH_IS_ZIP" = true ]; then
            # Test: Flash all partitions from zip (auto-reboots)
            # Note: Flash now verifies SSH connectivity and device info after reboot
            run_test "Flash firmware (all partitions, auto-reboot)" $TIMEOUT_FLASH $GO_FASTBOOT flash "$FLASH_PATH"

            # Flash automatically reboots to Linux and waits for SSH to be ready
            # The flash command now includes SSH verification, so device should be fully ready
            sleep 5

            # Verify device is actually in Linux mode and SSH is working
            log_info "Running: Verify Linux mode after flash (SSH should be ready)"
            TESTS_RUN=$((TESTS_RUN + 1))
            output=$($GO_FASTBOOT status 2>&1) && exit_code=0 || exit_code=$?
            if [ $exit_code -eq 0 ] && echo "$output" | grep -q "LINUX"; then
                log_pass "Verify Linux mode after flash (SSH verified)"
            else
                log_fail "Verify Linux mode after flash - status check failed"
                [ "$VERBOSE" = false ] && echo "  Output: $output"
            fi
        else
            # Test: Specific partition flash (no auto-reboot)
            run_test "Flash firmware (specific partition, no auto-reboot)" $TIMEOUT_FLASH $GO_FASTBOOT flash "$FLASH_PATH"

            # Device should still be in fastboot mode (no reboot for targeted updates)
            sleep 2

            log_info "Running: Verify still in fastboot mode (no auto-reboot for targeted flash)"
            TESTS_RUN=$((TESTS_RUN + 1))
            output=$($GO_FASTBOOT status 2>&1) && exit_code=0 || exit_code=$?
            if [ $exit_code -eq 0 ] && echo "$output" | grep -q "FASTBOOT"; then
                log_pass "Verify still in fastboot mode after specific partition flash"
            else
                log_fail "Verify fastboot mode - device should still be in FASTBOOT mode"
                [ "$VERBOSE" = false ] && echo "  Output: $output"
            fi

            # Manually reboot to Linux
            run_test "Exit fastboot mode after flash" $TIMEOUT_EXIT $GO_FASTBOOT exit

            # Verify back in Linux mode
            sleep 5
            log_info "Running: Verify Linux mode after manual reboot"
            TESTS_RUN=$((TESTS_RUN + 1))
            output=$($GO_FASTBOOT status 2>&1) && exit_code=0 || exit_code=$?
            if [ $exit_code -eq 0 ] && echo "$output" | grep -q "LINUX"; then
                log_pass "Verify Linux mode after manual reboot"
            else
                log_fail "Verify Linux mode - status check failed"
                [ "$VERBOSE" = false ] && echo "  Output: $output"
            fi
        fi
    else
        # No flash - manually exit fastboot mode
        run_test "Exit fastboot mode (reboot to Linux)" $TIMEOUT_EXIT $GO_FASTBOOT exit

        # Give device a moment to start rebooting
        sleep 2

        # Verify back in Linux mode (retry for up to 60s)
        log_info "Running: Verify Linux mode (with retries)"
        TESTS_RUN=$((TESTS_RUN + 1))
        RETRY_TIMEOUT=60
        RETRY_INTERVAL=5
        RETRY_ELAPSED=0
        RETRY_SUCCESS=false

        while [ $RETRY_ELAPSED -lt $RETRY_TIMEOUT ]; do
            if [ "$VERBOSE" = true ]; then
                echo "  \$ $GO_FASTBOOT status (attempt at ${RETRY_ELAPSED}s)"
            fi
            output=$($GO_FASTBOOT status 2>&1) || true
            if echo "$output" | grep -q "LINUX"; then
                RETRY_SUCCESS=true
                break
            fi
            sleep $RETRY_INTERVAL
            RETRY_ELAPSED=$((RETRY_ELAPSED + RETRY_INTERVAL))
        done

        if [ "$RETRY_SUCCESS" = true ]; then
            log_pass "Verify Linux mode (${RETRY_ELAPSED}s)"
        else
            log_fail "Verify Linux mode - device not in LINUX mode after ${RETRY_TIMEOUT}s"
            [ "$VERBOSE" = false ] && echo "  Last output: $output"
        fi
    fi

    # Test: Repeat fastboot entry after returning to Linux
    log_info "Testing repeated fastboot entry..."
    run_test "Enter fastboot again" $TIMEOUT_ENTER $GO_FASTBOOT enter

    # Exit back to Linux
    run_test "Final exit to Linux" $TIMEOUT_EXIT $GO_FASTBOOT exit
fi

#
# Summary
#

echo ""
echo "============================================"
echo "Test Summary"
echo "============================================"
echo -e "Tests run:    $TESTS_RUN"
echo -e "Tests passed: ${GREEN}$TESTS_PASSED${NC}"
echo -e "Tests failed: ${RED}$TESTS_FAILED${NC}"
echo ""

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed.${NC}"
    exit 1
fi
