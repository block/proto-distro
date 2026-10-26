# TF-A secure-boot customisations

## Why

Decouple the ROT used by the BootROM to verify the FSBL from the ROT used
by TF-A to verify the rest of the boot chain (FIP, OPTEE, …). The two can
now be different keys, so the FSBL/BootROM trust anchor can rotate
independently of the FIP chain. This lets us ship one FIP/OPTEE that runs
on both fielded and newly-manufactured Control Boards, while still
enforcing anti-rollback on new devices.

## Keys

| Surface             | Key      | Verified by    |
|---------------------|----------|----------------|
| FSBL image (new HW) | **ROT2** | STM32MP13 BootROM |
| FIP + OPTEE         | **ROT1** | TF-A BL2       |

FSBL and FIP are signed with different keys; rotating one does not force
rotating the other. ROT2 is only trusted by the BootROM on
newly-manufactured devices, so old FSBLs cannot run there. ROT1 is
trusted by every device's pubkey-hash table, so the same FIP boots
everywhere.

Shared key settings and counter defaults are in `conf/machine/include/secureboot.inc`:

```bitbake
TFA_SIGN_KEY_FILE="privateKey01.pem"   # ROT2
FIP_SIGN_KEY_FILE="privateKey00.pem"   # ROT1
TFW_NVCTR_VAL ?= "1"
NTFW_NVCTR_VAL ?= "1"
```

C3 overrides both counters in `conf/machine/c3-p1.conf`:

```bitbake
TFW_NVCTR_VAL = "2"
NTFW_NVCTR_VAL = "2"
```

Change the C3 settings for C3 rotations. C2 inherits the shared defaults.

## How TF-A picks ROT1 as the FIP ROTPK

The BootROM rejects unknown header extensions, so `fsbl_signer` stashes
the FIP key index inside the existing padding extension payload:

```
offset 0  "ST\xFF\xFF"      padding ext magic
offset 4  44                 ext length
offset 8  "PROT"             proto marker
offset 12 fip_key_idx (u32)  index into pk_hashes[]
offset 16 random             filler
```

Patch 0007 reads the marker and returns `pk_hashes[fip_key_idx]` as the
ROTPK hash. No marker → stock behaviour (use the auth-ext's own `pk_idx`).

## Anti-rollback

Upstream TF-A stores the NV counter in a TAMP backup register, which only
survives reset when the chip has a battery-backed domain. Our Control
Boards have no such backup battery, so the register clears on every power
cycle and anti-rollback is effectively disabled. Patch 0006 replaces it
with `popcount(OTP[11])` — OTP is permanent. TF-A rejects certificates whose
counter is below the device counter. `plat_set_nv_ctr` advances OTP by
burning consecutive bits when a higher counter is accepted during boot.
It rejects targets above 32 and non-sequential OTP patterns. After the
counter advances, firmware with a lower certificate counter cannot boot.
Use `lock_ctrl nvctr_read` to read the device counter.

`cert_create` in `fip-utils-stm32mp.bbclass` hardcodes `--tfw-nvctr 0
--ntfw-nvctr 0`. `CERTTOOL` is overridden to `cert_create_nvctr_wrap.sh`,
which rewrites those two flag values from `TFW_NVCTR_VAL` /
`NTFW_NVCTR_VAL` before execing the real `cert_create`.

## Files

```
tf-a-stm32mp_%.bbappend   SRC_URI, CERTTOOL override, sign_tf_a_fw task.
files/
  0006-*.patch            NV counter from OTP[11] popcount.
  0007-*.patch            Read fip_key_idx from padding "PROT" marker.
  cert_create_nvctr_wrap.sh  Rewrite cert_create nvctr flags from env.
  0001..0005-*.patch      Pre-existing board/PMIC patches.
```

## Bumping the C3 counter

- Raise `TFW_NVCTR_VAL` and `NTFW_NVCTR_VAL` in `conf/machine/c3-p1.conf`.
- Set both C3 counters in `python/cot_signer/constants.py` to the same value.
- Promote the matching five production certificates from
  `recipes-dev/miner-signing-keys/files/prod/certs/prod/c3/rotations/nv-<N>/`
  to its `c3/` parent. Reissue the five development certificates at the same counter.
- Update the C3 counter arguments in
  `recipes-dev/miner-signing-keys/files/regenerate-config-certs.sh`.
- Verify both chains with `cot_signer verify-cot-partial` against their public keys
  and reference configs, and run the CoT tests.
- Update the firmware-signer submodule and counter test expectations, then rebuild
  its Lambda images. Build and test the C3 firmware before rollout. Booting it can
  permanently advance OTP and prevent rollback to earlier firmware.

## See also

- `python/fsbl/fsbl_signer.py` — `--fip-sk` stamps the "PROT" marker.
- `recipes-security/miner-optee-ta/.../lock_ctrl_ta/` — `nvctr_read`.
- <https://wiki.st.com/stm32mpu/wiki/STM32_header_for_binary_files>
