require miner-image-minimal.bb
inherit swupdate

DESCRIPTION = "Adds SWUpdate support on top of the minimal image."
IMAGE_INSTALL:append = " proto-swupdate"
SWUPDATE_SRC_URI_EXCLUDE += "sw-description.in sw-with-fip-description.in bootstrap-hook.sh fip-post-install.sh"

SRC_URI = "\
    file://sw-description.in \
    file://sw-with-fip-description.in \
    file://bootstrap-hook.sh \
    file://fip-post-install.sh \
"

# images and files that will be included in the .swu image
SWUPDATE_IMAGES = "${IMAGE_NAME}.rootfs.ext4.gz"
SWUPDATE_FIP_IMAGE = "fip/fip-${FIP_DEVICETREE_OPTEE}-optee${FIP_SIGN_SUFFIX}.bin"

DEPENDS += "miner-dev-swupdate-keys-native"

do_swuimage[depends] += "${IMAGE_BASENAME}:do_image_complete virtual/trusted-firmware-a:do_deploy proto-swupdate:do_deploy"
do_swuimage[vardeps] += "MEMFAULT_SOFTWARE_VERSION MEMFAULT_HARDWARE_VERSION VARIANT UBOOT_ENV_PARTITION_A UBOOT_ENV_PARTITION_B SWUPDATE_IMAGES"

python do_swuimage() {
    import hashlib
    import shutil
    import stat
    import subprocess
    from pathlib import Path

    def require_payload(path, executable=False):
        mode = path.lstat().st_mode
        if not stat.S_ISREG(mode) or path.stat().st_size == 0 or (executable and not mode & 0o111):
            bb.fatal("SWU payload must be a nonempty regular %sfile: %s" %
                     ("executable " if executable else "", path))

    workdir = Path(d.getVar("WORKDIR"))
    deploy = Path(d.getVar("SWUDEPLOYDIR"))
    image_name = d.getVar("IMAGE_NAME")
    image_link = d.getVar("IMAGE_LINK_NAME")
    images = d.getVar("SWUPDATE_IMAGES")
    if len(images.split()) != 1:
        bb.fatal("SWU requires exactly one configured rootfs")
    trees = (d.getVar("FIP_DEVICETREE_OPTEE") or "").split()
    if len(trees) != 1 or "/" in trees[0] or trees[0] in (".", ".."):
        bb.fatal("SWU requires one FIP_DEVICETREE_OPTEE token")

    for suffix in ("", "-with-fip"):
        stage = workdir / ("swu" + suffix)
        bb.utils.remove(str(stage), recurse=True)
        stage.mkdir()
        archive_data = bb.data.createCopy(d)
        archive_data.setVar("SWUPDATE_IMAGES", images)
        archive_data.setVar("S", str(stage))
        archive_data.setVar("IMAGE_NAME", image_name + suffix)
        archive_data.setVar("IMAGE_LINK_NAME", image_link + suffix if image_link else "")
        # Both archives share rootfs policy; the companion adds only hooks and FIP.
        description = (workdir / "sw-description.in").read_text()
        scripts = ""
        fip_description = ""
        if suffix:
            scripts = '''scripts: (
                    {
                        filename = "rootfs-post-install.sh";
                        sha256 = "$swupdate_get_sha256(rootfs-post-install.sh)";
                        type = "postinstall";
                    }
                );'''
            fip_description = (workdir / "sw-with-fip-description.in").read_text()
        description = description.replace("@@SWUPDATE_ROOTFS_SCRIPTS@@", scripts)
        description = description.replace("@@SWUPDATE_FIP_DESCRIPTION@@", fip_description)
        (stage / "sw-description").write_text(description)

        members = ["sw-description"]
        if archive_data.getVar("SWUPDATE_SIGNING"):
            members.append("sw-description.sig")
        if not archive_data.getVar("INHIBIT_SWUPDATE_ADD_SRC_URI"):
            swupdate_add_src_uri(archive_data, members)
        swupdate_add_artifacts(archive_data, members)

        if suffix:
            fip = Path(d.getVar("DEPLOY_DIR_IMAGE")) / d.getVar("SWUPDATE_FIP_IMAGE")
            updater = Path(d.getVar("DEPLOY_DIR_IMAGE")) / "proto-swupdate"
            require_payload(fip)
            require_payload(updater, executable=True)
            shutil.copyfile(fip, stage / "fip-ota.bin")
            shutil.copyfile(updater, stage / "proto-swupdate")
            shutil.copyfile(workdir / "fip-post-install.sh", stage / "fip-post-install.sh")
            hook = (workdir / "bootstrap-hook.sh").read_text()
            if hook.count("@UPDATER_SHA256@") != 1:
                bb.fatal("Bootstrap hook requires exactly one updater hash token")
            updater_hash = hashlib.sha256((stage / "proto-swupdate").read_bytes()).hexdigest()
            (stage / "rootfs-post-install.sh").write_text(hook.replace("@UPDATER_SHA256@", updater_hash))
            for member in ("fip-post-install.sh", "proto-swupdate", "rootfs-post-install.sh"):
                (stage / member).chmod(0o755)
            members.extend(["fip-ota.bin", "fip-post-install.sh", "proto-swupdate", "rootfs-post-install.sh"])

        prepare_sw_description(archive_data)
        for member in members:
            require_payload(stage / member)
        output = deploy / (archive_data.getVar("IMAGE_NAME") + ".swu")
        with output.open("wb") as archive:
            subprocess.run(["cpio", "-ov", "-H", "crc", "--reproducible"],
                           input=("\n".join(members) + "\n").encode(),
                           cwd=str(stage), stdout=archive, check=True)
        require_payload(output)
        if image_link and image_link != image_name:
            (deploy / (image_link + suffix + ".swu")).symlink_to(output.name)
}
