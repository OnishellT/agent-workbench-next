import { expect, test, type Page } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { BASE, requireRelease, signIn, watchConsole, type ConsoleGuard } from "./helpers";

// HO-11: the route/WireGuard diagnostic is read-only. The browser asks the real
// backend, the backend asks the real kernel with `ip -j`, and the screen shows the
// interface the kernel observed. No route is installed, removed or reconfigured.
//
// Preconditions, prepared by the owner of this run (never defaults):
//   E2E_SSH_ADDRESS / E2E_SSH_PORT / E2E_SSH_USER / E2E_SSH_KEY_PATH
//     a NEW reachable sshd fixture with a synthetic key
//   E2E_SSH_FINGERPRINT   its independent public host-key fingerprint
//   E2E_ROUTED_TARGET     an address routed away from loopback (valid recovery)
// Optional: E2E_WIREGUARD_TARGET, a target whose real route uses a WireGuard link.
//
//   E2E_BASE_URL=http://127.0.0.1:19066 E2E_ACCESS_TOKEN=... \
//     bun run test tests/e2e/hosts-wireguard-diagnostic-release.spec.ts

const HOSTS_ROUTE = process.env.E2E_HOSTS_ROUTE ?? "/?section=hosts";

interface ApiHost {
  id: string;
  name?: string;
  address?: string;
  observed_fingerprint?: string;
}
interface ApiRoute {
  host_id?: string;
  host_name?: string;
  address?: string;
  interface?: string | null;
  wireguard?: boolean;
  mtu?: number | null;
  main_default_uses_wireguard?: boolean;
  scope?: string;
  configuration_changed?: boolean;
  error?: string;
}
interface IPRoute {
  dst?: string;
  dev?: string;
  gateway?: string;
  table?: string;
  type?: string;
  scope?: string;
  metric?: number;
  prefsrc?: string;
}
interface IPLink {
  ifname?: string;
  mtu?: number;
  flags?: string[];
  operstate?: string;
}
interface IPAddr {
  ifname?: string;
  flags?: string[];
  mtu?: number;
  addr_info?: { family?: string; local?: string; prefixlen?: number }[];
}

const errors: string[] = [];
let guard: ConsoleGuard | null = null;

test.beforeEach(() => {
  errors.length = 0;
  guard = null;
});

test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
});

function required(name: string) {
  const value = process.env[name];
  if (!value)
    throw new Error(
      `${name} is not set: prepare the fresh HO-11 fixture for this run (no default, no reuse).`,
    );
  return value;
}

function sshFixture() {
  return {
    address: required("E2E_SSH_ADDRESS"),
    port: Number(required("E2E_SSH_PORT")),
    user: required("E2E_SSH_USER"),
    keyPath: required("E2E_SSH_KEY_PATH"),
    fingerprint: required("E2E_SSH_FINGERPRINT"),
  };
}

function ipJSON<T>(args: string[]): T {
  return JSON.parse(
    execFileSync("ip", ["-j", ...args], { encoding: "utf8" }),
  ) as T;
}

/** What the kernel really reports for one target, read the same way the backend does. */
function observedRoute(address: string, wireGuardLinks = ipJSON<IPLink[]>(["link", "show", "type", "wireguard"])) {
  const family = address.includes(":") ? "-6" : "-4";
  const routes = ipJSON<IPRoute[]>([family, "route", "get", address]);
  const defaults = ipJSON<IPRoute[]>([family, "route", "show", "default"]);
  const links = new Map(
    wireGuardLinks.flatMap((link) =>
      link.ifname ? [[link.ifname, link.mtu ?? null] as const] : [],
    ),
  );
  const dev = routes[0]?.dev ?? null;
  return {
    interface: dev,
    wireguard: dev !== null && links.has(dev),
    mtu: dev === null ? null : (links.get(dev) ?? null),
    main_default_uses_wireguard: defaults.some(
      (route) => route.dev !== undefined && links.has(route.dev),
    ),
  };
}

/** Stable network configuration: names, flags, addresses and routes only. Link
 *  counters are deliberately excluded because SSH traffic changes them. */
function networkConfiguration() {
  const links = ipJSON<IPLink[]>(["link", "show"]).map((link) => ({
    ifname: link.ifname,
    mtu: link.mtu,
    flags: link.flags,
    operstate: link.operstate,
  }));
  const addresses = ipJSON<IPAddr[]>(["addr", "show"]).map((link) => ({
    ifname: link.ifname,
    flags: link.flags,
    mtu: link.mtu,
    addresses: (link.addr_info ?? []).map((address) => ({
      family: address.family,
      local: address.local,
      prefixlen: address.prefixlen,
    })),
  }));
  const routes = ipJSON<IPRoute[]>(["route", "show", "table", "all"]).map(
    (route) => ({
      dst: route.dst,
      dev: route.dev,
      gateway: route.gateway,
      table: route.table,
      type: route.type,
      scope: route.scope,
      metric: route.metric,
      prefsrc: route.prefsrc,
    }),
  );
  return {
    links: links.sort((a, b) => (a.ifname ?? "").localeCompare(b.ifname ?? "")),
    addresses: addresses.sort((a, b) => (a.ifname ?? "").localeCompare(b.ifname ?? "")),
    routes: routes.sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b))),
  };
}

async function hosts(page: Page) {
  const response = await page.request.get("/api/hosts");
  expect(response.status()).toBe(200);
  // The release envelope is the contract this spec verifies.
  const body = (await response.json()) as { hosts?: ApiHost[] };
  return body.hosts ?? [];
}

async function createHost(
  page: Page,
  input: {
    name: string;
    address: string;
    user: string;
    port: number;
    keyPath: string;
    enabled: boolean;
  },
) {
  const response = await page.request.post("/api/hosts", {
    headers: { Origin: BASE ?? "" },
    data: {
      name: input.name,
      address: input.address,
      user: input.user,
      port: input.port,
      auth: "key",
      key_path: input.keyPath,
      enabled: input.enabled,
    },
  });
  expect(response.status(), `alta de ${input.name}`).toBe(201);
  const body = (await response.json()) as ApiHost;
  return body;
}

async function openHosts(page: Page) {
  requireRelease();
  guard = watchConsole(page);
  // The rejected diagnostics are negative cases of this spec and the screen shows
  // their 400 as its own alert.
  guard.expected.push("/api/network/wireguard");
  await signIn(page);
  await page.goto(HOSTS_ROUTE);
  await expect(
    page.getByRole("heading", { name: "Equipos SSH · Magpie e historial" }),
  ).toBeVisible();
}

function row(page: Page, host: ApiHost) {
  return page.locator(`[data-host="${host.id}"]`);
}

async function checkRoute(page: Page, host: ApiHost) {
  const response = page.waitForResponse(
    (candidate) =>
      candidate.url().endsWith(`/api/network/wireguard?host_id=${host.id}`) &&
      candidate.request().method() === "GET",
  );
  await row(page, host)
    .getByRole("button", { name: "Comprobar VPN", exact: true })
    .click();
  return await response;
}

async function expectRouteMessage(page: Page, host: ApiHost, body: ApiRoute) {
  if (body.wireguard) {
    await expect(row(page, host)).toContainText(`Ruta por ${body.interface}`);
    await expect(row(page, host)).toContainText(`MTU ${body.mtu}`);
  } else {
    await expect(row(page, host)).toContainText(`La conexión SSH usa ${body.interface}`);
    await expect(row(page, host)).toContainText(
      "revisa la IP de WireGuard del host",
    );
  }
}

test("la ruta real se consulta desde la UI sin mutar la red y las entradas inválidas se rechazan", async ({
  page,
}) => {
  await openHosts(page);
  const before = networkConfiguration();
  const fixture = sshFixture();
  const routedTarget = required("E2E_ROUTED_TARGET");
  const wireGuardTarget = process.env.E2E_WIREGUARD_TARGET;
  const alias = `ho11-${Date.now()}`;

  const ssh = await createHost(page, {
    name: `${alias}-ssh`,
    address: fixture.address,
    user: fixture.user,
    port: fixture.port,
    keyPath: fixture.keyPath,
    enabled: true,
  });
  const routed = await createHost(page, {
    name: `${alias}-routed`,
    address: routedTarget,
    user: fixture.user,
    port: 22,
    keyPath: fixture.keyPath,
    enabled: false,
  });
  const negatives = [
    { name: `${alias}-loopback`, address: "127.0.0.1" },
    { name: `${alias}-multicast`, address: "224.0.0.1" },
    { name: `${alias}-unspecified`, address: "0.0.0.0" },
    { name: `${alias}-hostname`, address: "ho11.invalid" },
  ];
  const rejected: ApiHost[] = [];
  for (const input of negatives)
    rejected.push(
      await createHost(page, {
        ...input,
        user: fixture.user,
        port: 22,
        keyPath: fixture.keyPath,
        enabled: false,
      }),
    );

  // The SSH fixture really answered a handshake: the registry observed its new key.
  await expect
    .poll(
      async () =>
        (await hosts(page)).find((candidate) => candidate.id === ssh.id)
          ?.observed_fingerprint ?? "",
      { timeout: 30_000, message: "handshake real de la fixture SSH" },
    )
    .toBe(fixture.fingerprint);

  await page.reload();
  await expect(row(page, ssh)).toBeVisible();

  const wireGuardLinks = ipJSON<IPLink[]>(["link", "show", "type", "wireguard"]);
  const routedExpected = observedRoute(routedTarget, wireGuardLinks);
  const routedResponse = await checkRoute(page, routed);
  expect(routedResponse.status()).toBe(200);
  const routedBody = (await routedResponse.json()) as ApiRoute;
  expect(routedBody).toMatchObject({
    host_id: routed.id,
    host_name: routed.name,
    address: routedTarget,
    scope: "route-only",
    configuration_changed: false,
  });
  expect(routedBody.interface).toBe(routedExpected.interface);
  expect(routedBody.wireguard).toBe(routedExpected.wireguard);
  expect(routedBody.mtu).toBe(routedExpected.mtu);
  expect(routedBody.main_default_uses_wireguard).toBe(
    routedExpected.main_default_uses_wireguard,
  );
  await expectRouteMessage(page, routed, routedBody);

  const sshExpected = observedRoute(fixture.address, wireGuardLinks);
  const sshResponse = await checkRoute(page, ssh);
  expect(sshResponse.status()).toBe(200);
  const sshBody = (await sshResponse.json()) as ApiRoute;
  expect(sshBody).toMatchObject({
    host_id: ssh.id,
    host_name: ssh.name,
    address: fixture.address,
    scope: "route-only",
    configuration_changed: false,
  });
  expect(sshBody.interface).toBe(sshExpected.interface);
  expect(sshBody.wireguard).toBe(sshExpected.wireguard);
  expect(sshBody.mtu).toBe(sshExpected.mtu);
  expect(JSON.stringify(sshBody)).not.toContain("PRIVATE KEY");
  await expectRouteMessage(page, ssh, sshBody);

  if (wireGuardLinks.length > 0 && wireGuardTarget) {
    const wireGuard = await createHost(page, {
      name: `${alias}-wireguard`,
      address: wireGuardTarget,
      user: fixture.user,
      port: 22,
      keyPath: fixture.keyPath,
      enabled: false,
    });
    await page.reload();
    const expected = observedRoute(wireGuardTarget, wireGuardLinks);
    const response = await checkRoute(page, wireGuard);
    expect(response.status()).toBe(200);
    const body = (await response.json()) as ApiRoute;
    expect(body).toMatchObject({
      host_id: wireGuard.id,
      host_name: wireGuard.name,
      address: wireGuardTarget,
      scope: "route-only",
      configuration_changed: false,
    });
    expect(body.interface).toBe(expected.interface);
    expect(body.wireguard).toBe(true);
    expect(body.mtu).toBe(expected.mtu);
    expect(body.main_default_uses_wireguard).toBe(
      expected.main_default_uses_wireguard,
    );
    await expectRouteMessage(page, wireGuard, body);
  } else if (wireGuardLinks.length === 0) {
    // The kernel has no WireGuard interface: no diagnostic may invent a tunnel.
    expect(routedBody.wireguard).toBe(false);
    expect(sshBody.wireguard).toBe(false);
  }

  for (const [index, host] of rejected.entries()) {
    const response = await checkRoute(page, host);
    expect(response.status(), negatives[index].name).toBe(400);
    const body = (await response.json()) as ApiRoute;
    const expected =
      index === negatives.length - 1
        ? "no se resuelven nombres"
        : "unspecified, multicast ni loopback";
    expect(body.error).toContain(expected);
    await expect(row(page, host).getByRole("alert")).toHaveText(body.error ?? "");
    await expect(row(page, host)).not.toContainText("Ruta por ");
    await expect(row(page, host)).not.toContainText("La conexión SSH usa ");
  }

  // Recovery in the same browser: the rejected loopback host is edited to a real
  // routed address and the same diagnostic succeeds. The host record survives reload.
  const recovered = rejected[0];
  await row(page, recovered).getByRole("button", { name: "Editar", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("IP o nombre de host").fill(routedTarget);
  await dialog.getByRole("button", { name: "Guardar cambios", exact: true }).click();
  await expect(dialog).toBeHidden();
  await expect
    .poll(
      async () =>
        (await hosts(page)).find((candidate) => candidate.id === recovered.id)
          ?.address ?? "",
      { message: "dirección recuperada persistida" },
    )
    .toBe(routedTarget);

  await page.reload();
  await expect(row(page, recovered)).toBeVisible();
  const recoveredResponse = await checkRoute(page, recovered);
  expect(recoveredResponse.status()).toBe(200);
  const recoveredBody = (await recoveredResponse.json()) as ApiRoute;
  expect(recoveredBody).toMatchObject({
    host_id: recovered.id,
    host_name: recovered.name,
    address: routedTarget,
    scope: "route-only",
    configuration_changed: false,
  });
  expect(recoveredBody.interface).toBe(routedExpected.interface);
  expect(recoveredBody.wireguard).toBe(routedExpected.wireguard);
  await expectRouteMessage(page, recovered, recoveredBody);

  // The diagnostic left every interface, address and route exactly as it found them.
  expect(networkConfiguration()).toEqual(before);
});
