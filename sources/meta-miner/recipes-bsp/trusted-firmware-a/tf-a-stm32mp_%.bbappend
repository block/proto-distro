FILESEXTRAPATHS:prepend:="${THISDIR}/files:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/cx-common:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/cx-common/tf-a:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c2:${THISDIR}/../../dts/c2/tf-a:"
FILESEXTRAPATHS:prepend:="${THISDIR}/../../dts/c3:${THISDIR}/../../dts/c3/tf-a:"

DEPENDS:append = "${@bb.utils.contains('TF_A_SIGN_ENABLE', '1', ' miner-dev-stm32mp1-keys-native', '', d)}"

SRC_URI += "file://c2 \
            file://c2/tf-a \
            file://c3 \
            file://c3/tf-a \
            file://cx-common \
            file://cx-common/tf-a \
            file://0001-enable-i2c2-tf-a-stm32mp.patch \
            file://0002-mp135f-dk-pmic-to-i2c2.patch \
            file://0003-tf-a-pmic-en-ldo1-ldo2-hack.patch \
            file://0004-tf-a-pmic-rreq-en-ponkey-10s.patch \
            file://0005-tf-a-pmic-dump-register.patch \
            file://0006-tf-a-bypass-nvcounter-rollback-check.patch \
            file://0007-tf-a-proto-auth-fip-key-idx.patch \
            file://cert_create_nvctr_wrap.sh \
"

# Intercept cert_create in fip-utils-stm32mp.bbclass (which hardcodes
# --tfw-nvctr 0 --ntfw-nvctr 0). The wrapper rewrites those two flag values to
# ${TFW_NVCTR_VAL} / ${NTFW_NVCTR_VAL} from secureboot.inc, then execs the real
# cert_create from tf-a-tools-native. ${CERTTOOL} is expanded unquoted in
# shell, so the inline `env` prefix splits into tokens correctly.
CERTTOOL = "env CERT_CREATE_REAL=${STAGING_BINDIR_NATIVE}/cert_create \
TFW_NVCTR_VAL=${TFW_NVCTR_VAL} \
NTFW_NVCTR_VAL=${NTFW_NVCTR_VAL} \
${WORKDIR}/cert_create_nvctr_wrap.sh"

do_configure:append() {
    # Note: The dts files must be copied to the fdts directory.
    # The tf-a build system expects them to be in this directory
    # => fdts/${dt}-${FWCONFIG_NAME}.dts
    # => fdts/stm32mp15-mx.dtsi, fdts/stm32mp13-mx.dtsi

    # Install the C2 common dtsi files
    cp ${WORKDIR}/c2/tf-a/stm32mp131f-c2-common.dtsi ${S}/fdts
    # Install the C2 Proto1 dts files
    cp ${WORKDIR}/c2/tf-a/stm32mp131f-c2p1.dts ${S}/fdts
    cp ${WORKDIR}/cx-common/tf-a/stm32mp131f-cx-fw-config.dts ${S}/fdts/stm32mp131f-c2p1-fw-config.dts
    # Install the C3 Proto1 dts files
    cp ${WORKDIR}/c3/tf-a/stm32mp131f-c3p1.dts ${S}/fdts
    cp ${WORKDIR}/cx-common/tf-a/stm32mp131f-cx-fw-config.dts ${S}/fdts/stm32mp131f-c3p1-fw-config.dts
    # Install the C2 EVT dts files
    cp ${WORKDIR}/c2/tf-a/stm32mp131f-c2evt.dts ${S}/fdts
    cp ${WORKDIR}/cx-common/tf-a/stm32mp131f-cx-fw-config.dts ${S}/fdts/stm32mp131f-c2evt-fw-config.dts
    # Install the common Cx dts and pin control files
    cp ${WORKDIR}/cx-common/stm32mp131f-cx.dtsi ${S}/fdts
    # Install the common tfa dts files
    cp ${WORKDIR}/cx-common/tf-a/stm32mp13-mx.dtsi ${S}/fdts
    cp ${WORKDIR}/cx-common/tf-a/stm32mp15-mx.dtsi ${S}/fdts
}

python sign_tf_a_fw() {
    import subprocess
    import glob
    import binascii

    if not bb.utils.which(os.getenv('PATH'), 'fsbl_signer'):
        bb.fatal("fsbl_signer not found in PATH.")

    product_family = d.getVar('PRODUCT_FAMILY')
    if not product_family:
        bb.fatal("PRODUCT_FAMILY not specified. Required for fsbl_signer!")

    tf_a_config = d.getVar('TF_A_CONFIG')
    tf_a_devicetree = d.getVar('TF_A_DEVICETREE').split(",")
    tf_a_binaries = d.getVar('TF_A_BINARIES').split(",")
    for i, cfg in enumerate(tf_a_config.split()):
        dt_config = tf_a_devicetree[i]
        tfa_basename = tf_a_binaries[i]
        for dt in dt_config.split():
            tfa_sign_key = ""
            socname = ""
            socname_list = d.getVar('STM32MP_SOC_NAME')
            if socname_list and len(socname_list) > 0:
                for soc in socname_list.split():
                    tfa_signingkey_name = 'TFA_SIGN_KEY_%s' % soc
                    tfa_sign_key = d.getVar(tfa_signingkey_name)
                    socname = soc
                    if not tfa_sign_key:
                        bb.fatal("Please make sure to configure \"TFA_SIGN_KEY_%s\" var to signing key file." % soc)
                    if not os.path.exists(tfa_sign_key):
                        bb.fatal("Unable to find %s." % tfa_sign_key)

            tf_a_bin = os.path.join(d.getVar('B'), "%s-%s" % (cfg, socname), "%s-%s-%s.%s" % (tfa_basename, dt, cfg, d.getVar('TF_A_SUFFIX')))
            tf_a_signed_bin = os.path.join(d.getVar('B'), "%s-%s" % (cfg, socname), "%s-%s-%s_Signed.%s" % (tfa_basename, dt, cfg, d.getVar('TF_A_SUFFIX')))
            signed_key_hashes = os.path.join(d.getVar('B'), "publicKeysHashHashes.bin")
            expected_key_hashes = os.path.join(d.getVar("STM32MP1_SIGN_KEYDIR"), "publicKeysHashHashes.bin")

            if not os.path.exists(tf_a_bin):
                # Skip this dt config, does not exist
                continue

            fsbl_signer_args = [ "--product", str(product_family).upper(), "-i", tf_a_bin, "-o", tf_a_signed_bin, "--sk", tfa_sign_key, "--pwd", d.getVar("TFA_SIGN_KEY_PASS"), "--pk-hashes-out", d.getVar('B') ]
            for pubkey in sorted(glob.glob(d.getVar("STM32MP1_SIGN_KEYDIR") + "/publicKey*.pem")):
                fsbl_signer_args += [ "--pk", pubkey ]

            # Stash the FIP signing key index in the FSBL padding so TF-A can
            # select the matching ROTPK from the auth pubkey-hash table.
            fip_sign_key = d.getVar('FIP_SIGN_KEY_%s' % socname)
            if fip_sign_key:
                if not os.path.exists(fip_sign_key):
                    bb.fatal("FIP signing key not found: %s" % fip_sign_key)
                fsbl_signer_args += [ "--fip-sk", fip_sign_key ]

            result = subprocess.run(["fsbl_signer", "sign"] + fsbl_signer_args, capture_output=True)
            if result.returncode != 0:
                bb.error(result.stderr.decode())
                bb.fatal("fsbl_signer failed with error code %d" % result.returncode)

            # Make sure the signer get the same publicKey key hashes, the order of --pk argument are important
            result = subprocess.run(["diff", signed_key_hashes, expected_key_hashes])
            if result.returncode != 0:
                hash_bin = binascii.b2a_hex(open(signed_key_hashes, "rb").read())
                bb.fatal("fsbl_signer failed with unexpected unexpected publicKey key hash! (%s)" % hash_bin)

            subprocess.run(f"install -m 644 {tf_a_signed_bin} {d.getVar("DEPLOYDIR")}/arm-trusted-firmware/", shell=True, check=True)
}

do_deploy[postfuncs] =+ "${@bb.utils.contains('TF_A_SIGN_ENABLE', '1', 'sign_tf_a_fw', '', d)}"

do_deploy:append() {
    # Export the dev certs generated used in signing
    if [ "${TF_A_SIGN_ENABLE}" = "1" ]; then
        install -d ${DEPLOYDIR}/arm-trusted-firmware/certs
        install -m 0755 ${WORKDIR}/nt_fw_content.crt ${DEPLOYDIR}/arm-trusted-firmware/certs
        install -m 0755 ${WORKDIR}/nt_fw_key.crt ${DEPLOYDIR}/arm-trusted-firmware/certs
        install -m 0755 ${WORKDIR}/nt_fw_key.crt ${DEPLOYDIR}/arm-trusted-firmware/certs
        install -m 0755 ${WORKDIR}/stm32mp_cfg_cert.crt ${DEPLOYDIR}/arm-trusted-firmware/certs
        install -m 0755 ${WORKDIR}/tb_fw.crt ${DEPLOYDIR}/arm-trusted-firmware/certs
        install -m 0755 ${WORKDIR}/tos_fw_content.crt ${DEPLOYDIR}/arm-trusted-firmware/certs
        install -m 0755 ${WORKDIR}/tos_fw_key.crt ${DEPLOYDIR}/arm-trusted-firmware/certs
        install -m 0755 ${WORKDIR}/trusted_key.crt ${DEPLOYDIR}/arm-trusted-firmware/certs
    fi
}

