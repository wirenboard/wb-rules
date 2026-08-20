// The TypeScript test suites need a NATIVE tsgo in the build chroot (the
// tests run on the build architecture; a foreign-arch binary would be
// qemu-emulated). The wb8/wb6 release repos publish arm only, so the
// compiler comes from the quickjs2 testing set, which wbdev adds as an
// sbuild extra-repository with amd64 enabled.
env.WBDEV_TESTING_SETS = 'quickjs2'

buildDebGolangWbgo defaultTargets: 'current-armhf current-arm64',
                   defaultGoVersion: '1.26',
                   defaultRunLintian: true
