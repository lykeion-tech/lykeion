import { createHash, createPublicKey } from 'node:crypto';

const stableVersion = /^(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})$/;

export function desktopGoEnvironment(input, trustedRelease = false) {
  return {
    ...input,
    GOFLAGS: '',
    GOENV: 'off',
    GOWORK: 'off',
    GOEXPERIMENT: '',
    ...(trustedRelease ? { GOTOOLCHAIN: 'local' } : {}),
    GOMAXPROCS: '2',
  };
}

export function updateBuildOptions(anchors, version, platform, arch, hostPlatform, hostArch) {
  if (anchors === undefined || anchors === '') return undefined;
  const invalid = () => {
    throw new Error('Invalid desktop update build configuration');
  };
  if (
    typeof anchors !== 'string' ||
    anchors.length > 512 ||
    !stableVersion.test(version) ||
    !['darwin', 'linux'].includes(platform) ||
    !['x64', 'arm64'].includes(arch) ||
    platform !== hostPlatform ||
    arch !== hostArch
  )
    invalid();
  const parts = anchors.split(',');
  if (parts.length < 1 || parts.length > 2) invalid();
  const keyIDs = [];
  for (const part of parts) {
    if (!/^[A-Za-z0-9+/]+={0,2}$/.test(part)) invalid();
    const der = Buffer.from(part, 'base64');
    if (der.toString('base64') !== part) invalid();
    let key;
    try {
      key = createPublicKey({ key: der, type: 'spki', format: 'der' });
    } catch {
      invalid();
    }
    if (
      key.asymmetricKeyType !== 'ed25519' ||
      !key.export({ type: 'spki', format: 'der' }).equals(der)
    )
      invalid();
    const id = createHash('sha256').update(der).digest('hex');
    if (keyIDs.includes(id)) invalid();
    keyIDs.push(id);
  }
  return { anchors, version, keyIDs };
}

export function updateBuildFlags(options) {
  if (!options) return [];
  return [
    '-ldflags',
    `-X=lykeion/server/internal/desktopupdate.embeddedVersion=${options.version} -X=lykeion/server/internal/desktopupdate.embeddedKeys=${options.anchors}`,
  ];
}

export function verifyUpdateBuildInfo(raw, options) {
  try {
    if (typeof raw !== 'string' || raw.length > 4096) throw new Error();
    const info = JSON.parse(raw);
    if (
      !info ||
      Object.keys(info).sort().join(',') !== 'keyIDs,version' ||
      info.version !== options.version ||
      !Array.isArray(info.keyIDs) ||
      JSON.stringify(info.keyIDs) !== JSON.stringify(options.keyIDs)
    )
      throw new Error();
  } catch {
    throw new Error('Built desktop update trust does not match release configuration');
  }
}
