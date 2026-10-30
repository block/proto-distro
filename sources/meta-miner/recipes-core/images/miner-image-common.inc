inherit core-image
inherit extrausers
require miner-image-base.bb

DESCRIPTION = "Shared image setup for release and manufacturing."

IMAGE_FEATURES += "ssh-server-openssh"

# admin user, sudoers, and /etc/passwd must be identical in both release
# and mfg rootfs's - any variant-specific user state written at runtime
# would get copy-up'd into the /data overlay and propagate to the other
# variant on cross-variant OTA.
EXTRA_USERS_PARAMS:append = "\
    useradd -u 1000 -d /home/admin -s /bin/sh -p '' admin; \
    usermod -a -G sudo admin; \
    "

# Bake the admin login password into the lower layer of both release and mfg
# rootfs's, so the credential never has to be written at runtime into the /data
# overlay (which would propagate across a cross-variant OTA). Done in
# IMAGE_PREPROCESS_COMMAND (not ROOTFS_POSTPROCESS_COMMAND) so it runs after
# extrausers' set_user_group has created the admin entry in /etc/shadow.
# Regenerate hash: openssl passwd -6 -salt protominer1 proto
python proto_set_admin_password() {
    import os
    rootfs = d.getVar('IMAGE_ROOTFS')
    shadow = os.path.join(rootfs, 'etc', 'shadow')
    pwhash = '$6$protominer1$Rf4nWS5tdTFYjVN5sI5kCHZN7l0qBpvgiveXEYENdovKra50mKF6a4m7q36/9N16Fo8425B0zGVZs9hiB8s5b0'
    with open(shadow, 'r') as f:
        lines = f.readlines()
    modified = 0
    with open(shadow, 'w') as f:
        for line in lines:
            if line.startswith('admin:'):
                parts = line.split(':')
                parts[1] = pwhash
                line = ':'.join(parts)
                modified += 1
            f.write(line)
    if modified != 1:
        bb.fatal("proto_set_admin_password: expected exactly 1 'admin:' line, updated %d" % modified)
}
IMAGE_PREPROCESS_COMMAND += " proto_set_admin_password ;"

IMAGE_INSTALL += "sudo miner-firstboot"

install_admin_sudoers() {
    install -d ${IMAGE_ROOTFS}${sysconfdir}/sudoers.d
    echo "admin ALL=(ALL:ALL) NOPASSWD: ALL" > ${IMAGE_ROOTFS}${sysconfdir}/sudoers.d/admin
    chmod 0440 ${IMAGE_ROOTFS}${sysconfdir}/sudoers.d/admin
}
ROOTFS_POSTPROCESS_COMMAND += " install_admin_sudoers ;"
