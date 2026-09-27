export function pageIdentity(url) {
  try {
    const parsed = new URL(url);
    parsed.hash = "";
    return parsed.href;
  } catch {
    return null;
  }
}

export function hostFromUrl(url) {
  try {
    const { protocol, hostname } = new URL(url);
    if (!["http:", "https:", "ws:", "wss:"].includes(protocol)) return null;
    return hostname.replace(/^\[|\]$/g, "").toLowerCase();
  } catch {
    return null;
  }
}

export function ipVersion(host) {
  if (!host) return 0;
  if (host.includes(":")) {
    try {
      return new URL(`http://[${host}]/`).hostname ? 6 : 0;
    } catch {
      return 0;
    }
  }
  const parts = host.split(".");
  if (parts.length !== 4 || parts.some((part) => !/^\d{1,3}$/.test(part) || Number(part) > 255)) return 0;
  return 4;
}

export function cidrRule(ip) {
  const version = ipVersion(ip);
  return version ? `IP-CIDR,${ip}/${version === 4 ? 32 : 128}` : null;
}

export function ruleOptions(host, connectedIps = [], registrableDomain, isKnownSuffix = () => false) {
  if (!host) return [];
  if (ipVersion(host)) return [{ label: `IP-CIDR · ${host}`, text: cidrRule(host) }];

  const options = [{ label: `DOMAIN · ${host}`, text: `DOMAIN,${host}` }];
  const base = registrableDomain?.(host);
  if (!isKnownSuffix(host)) {
    let candidate = host;
    while (candidate) {
      options.push({ label: `DOMAIN-SUFFIX · ${candidate}`, text: `DOMAIN-SUFFIX,${candidate}` });
      if (!base || candidate === base) break;
      const dot = candidate.indexOf(".");
      if (dot < 0) break;
      candidate = candidate.slice(dot + 1);
      if (!candidate.endsWith(`.${base}`) && candidate !== base) break;
    }
  }
  for (const ip of [...new Set(connectedIps)]) {
    const text = cidrRule(ip);
    if (text) options.push({ label: `IP-CIDR · ${ip}（实际连接）`, text });
  }
  return options;
}

export function uniqueRuleLines(lines) {
  return [...new Set(lines.filter(Boolean))].join("\n");
}
