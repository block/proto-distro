env = {
  "PATH": "${HERMIT_ENV}/bin/scripts:${HERMIT_ENV}/bin:${PATH}",
}
sources = ["env:///bin/hermit-packages", "https://github.com/cashapp/hermit-packages.git"]

github-token-auth {
}
