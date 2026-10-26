# Proto C3 CoT Rotations

Pre-generated production Chain-of-Trust key/config certificate sets for C3 secure
boot at `NV_Counter = 2, 3, 4, 5`, signed with the existing production ROT1 +
intermediate keys. The active chain in the parent directory uses `NV=2`.
The `NV=3, 4, 5` sets remain staged for future rollout.

- Source ceremony: SECENG-9601 (https://block.atlassian.net/browse/SECENG-9601)
- Signing chain: production ROT1 + world/FW intermediates, unchanged from SECENG-9528.
- Generated against the same pinned `regenerated-config-certs/{fw-config,hw-config-prod}.bin.reference`
  as the deployed `NV=1` chain, so no config regeneration is needed to promote.

## Active certificate selection

The FIP signer reads five certificates by fixed filename from the parent
`prod/certs/prod/c3/` directory only, non-recursively
(`btc-fw-signer` `fwsigner/signers/stm32mp1.py`). It never descends into
`rotations/`, and `prod/certs` is not referenced by any Yocto recipe `SRC_URI`,
so these files affect nothing until promoted.

## Promotion (one NV value per firmware release, forward only)

To roll forward from `NV=k` to `NV=k+1`:

1. Copy the five certs from `rotations/nv-<N>/` up into `prod/certs/prod/c3/`,
   overwriting the active set:
   ```bash
   cp rotations/nv-<N>/*.crt .
   ```
2. Bump the enforced counter in `python/cot_signer/constants.py` to `<N>`:
   ```python
   TRUSTED_NV_COUNTER[Product.C3]     = <N>
   NON_TRUSTED_NV_COUNTER[Product.C3] = <N>
   ```
3. Ship both changes together. The signer stamps content certs with the constant
   and `cot_signer verify-cot` enforces it against the key certs — a mismatch
   fails signing.

Do not promote non-consecutive NV values: devices enforce `NV_Counter >= chain`
and permanently burn the counter into OTP.

## Certificates per set

Each `nv-<N>/` holds the five CoT key/config certs (DER-encoded): `trusted_key.crt`,
`stm32mp_cfg_cert.crt`, `tb_fw.crt`, `tos_fw_key.crt`, `nt_fw_key.crt`. The
`*_content.crt` leaf certs are **not** included — the signer regenerates those per
firmware image from the actual bootloader hashes.

## SHA-256

### nv-2

```
4fd3cc81da469f9093b4d9bc987a6b1c76d4d2bc2b4d86e9c6cb9a70e6940ff8  trusted_key.crt
02ccdd53c16a6be53d330381c8de5f12b067647a785f11a903c49729881c967e  stm32mp_cfg_cert.crt
2ff7c453e947146798058ddf9f757ca01c0ad44135b00c4c406b49687ad6de55  tb_fw.crt
3518109973bcf57d02a115d0dc3b2d8dd7633aed0d18f3b52350e922c5ccb98e  tos_fw_key.crt
3ddef5671311e97ea715bb5b8939e5ff662d5ee8d4bf1b6cc25a178348e58902  nt_fw_key.crt
```

### nv-3

```
ad86217da18105e2a87c6d9d4d7050d26c0457a7ad8407c4cd4b2d68b167d913  trusted_key.crt
a6f7c37b46e9165bd97dea9ca3aff9b9e9a6531e4b49c62da2f61bddddb99eff  stm32mp_cfg_cert.crt
6a8d85ba9b50aff25dc16f0bb665dedb99fa19a066466511f8d1f4388f81c81a  tb_fw.crt
44cea2fbe6b0a5d64abf7a5124ff69fd6d051d90aaf37b3d78e7e4485dd35685  tos_fw_key.crt
51c14c5e6a7a59110ec12e40749d952ad26f60e1a1f559f6e839165ed72ededd  nt_fw_key.crt
```

### nv-4

```
37e3e3aa97f325ac207a87b3f2c3efbf3fc8a8f277c8c8f8c2eeb7f25202959a  trusted_key.crt
f06098fbc2a966cf46ee64ac8514f85a9963da954cacb622c77b1675b762a52f  stm32mp_cfg_cert.crt
ae336e06ff527a16ba6bc029de6322842af2961f4c333e4d3bc28b0f9800f7db  tb_fw.crt
2f717c4c89aa6267d881d0682a3b0a721080e6ab544deaaa37c5142976c64912  tos_fw_key.crt
1f5ec0fb6b306fbad6a9106fdaa5c1206d601d6ebc44cdfe168883daba3719c9  nt_fw_key.crt
```

### nv-5

```
dddb7b55b85c86fcd4c4eaa65772a2f0846d92a0a14df28ad1b752b5e8eb5933  trusted_key.crt
6dbcb28c7e0a1e72b2193eaa09f25e8fbe564368d83aa50ff747651b8200ccda  stm32mp_cfg_cert.crt
c32ff5563fe13501698a34a0f081726dc4a5356d811246939d61ef75c5507538  tb_fw.crt
a54a48a53a3765674cb26fa64e6a69637636e52e9033f8b74a7120077e8fc61f  tos_fw_key.crt
5f9676a1945a41698a71f7c52adbe9bcc9ad7e975f7139e2076b065494a2ebbb  nt_fw_key.crt
```
