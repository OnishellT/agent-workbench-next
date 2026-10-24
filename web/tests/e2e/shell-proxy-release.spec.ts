import { expect, test, type Page } from "@playwright/test";
import { BASE, TOKEN, watchConsole, type ConsoleGuard } from "./helpers";

// M02 PL-04: la shell tras un proxy TLS real (loopback, certificado propio de la
// fixture) mantiene su autoridad y su frontera de confianza. El certificado self-signed
// lo acepta SÓLO este contexto de prueba (ignoreHTTPSErrors); nunca se instala en el CA
// ni en el sistema. Los negativos van contra el MISMO entrypoint real y con autoridad
// propia: lo único que cambia es la condición bajo prueba.
//
//   E2E_BASE_URL=https://127.0.0.1:19013 E2E_ACCESS_TOKEN=... \
//   E2E_PROJECT_DIR=/.../a/projects/proxy-marker E2E_PROXY_LISTENER=http://127.0.0.1:19012 \
//   E2E_HOSTILE_HOST=workbench.localhost:19013
//     bun run test tests/e2e/shell-proxy-release.spec.ts
//
// El proxy TLS y sus certificados son de la fixture (work/pl010203/tls, 127.0.0.1:19013);
// el peer no confiado (127.0.0.2) se comprueba con el fixture desde la CLI, porque un
// navegador no puede elegir su dirección de origen.

let guard: ConsoleGuard | null = null;
test.beforeEach(({ page }) => {
  guard = watchConsole(page);
});
test.afterEach(() => {
  expect(guard?.errors ?? [], "consola del navegador").toEqual([]);
  guard = null;
});

interface ProxyFixture {
  token: string;
  project: string;
  listener: string;
  hostileHost: string;
}

function requiredProxyFixture(): ProxyFixture {
  const token = TOKEN;
  const project = process.env.E2E_PROJECT_DIR;
  const listener = process.env.E2E_PROXY_LISTENER;
  const hostileHost = process.env.E2E_HOSTILE_HOST;
  if (!BASE?.startsWith("https://"))
    throw new Error("E2E_BASE_URL must be the HTTPS origin of the fixture proxy (PL-04 is the TLS proxy path; HTTP is not a substitute).");
  if (!token) throw new Error("E2E_ACCESS_TOKEN is required: this case signs in for real.");
  if (!project || !listener || !hostileHost)
    throw new Error(
      "This case needs E2E_PROJECT_DIR (a folder of its own), E2E_PROXY_LISTENER (the app listener behind the proxy) and E2E_HOSTILE_HOST (a foreign Host value). No default, no fallback.",
    );
  for (const [name, value] of [
    ["E2E_PROJECT_DIR", project],
    ["E2E_PROXY_LISTENER", listener],
  ] as const) {
    if (!value.startsWith("/") && !value.startsWith("http"))
      throw new Error(`${name} must be absolute, got "${value}"`);
  }
  return { token, project, listener, hostileHost };
}

async function readProjects(page: Page) {
  const response = await page.request.get(new URL("/api/bootstrap", BASE!).href);
  expect(response.status()).toBe(200);
  const body = (await response.json()) as { settings?: { projects?: { path: string }[] } };
  return (body.settings?.projects ?? []).map((row) => row.path);
}

async function unlockProxy(page: Page, token: string) {
  await page.goto(BASE!);
  await page.locator("#access-token").fill(token);
  await page.getByRole("button", { name: "Entrar", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "Navegación principal" })).toBeVisible();
}

test("PL-04: la shell tras el proxy TLS usa cookie Secure y conserva su marcador", async ({
  browser,
}) => {
  test.setTimeout(240_000);
  const fixture = requiredProxyFixture();
  // Certificado de la fixture: sólo este contexto lo acepta.
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  await unlockProxy(page, fixture.token);

  // La cookie que la instalación emitió es Secure/HttpOnly/Strict y el script no la ve.
  const session = (await context.cookies(BASE!)).find((row) =>
    row.name.startsWith("workbench_session_"),
  );
  expect(session, "cookie de sesión del proxy TLS").toBeTruthy();
  expect(session!.secure, "Secure sobre HTTPS").toBe(true);
  expect(session!.httpOnly, "HttpOnly").toBe(true);
  expect(session!.sameSite, "SameSite=Strict").toBe("Strict");
  expect(await page.evaluate(() => document.cookie)).not.toContain("workbench_session");
  expect(page.url(), "sin secretos en la URL").not.toContain("token");

  // La fixture puede traer el marcador de una corrida anterior: se quita con el mismo
  // control para que el caso sea idempotente y la instalación quede limpia.
  await page.goto("/?section=settings");
  if ((await readProjects(page)).includes(fixture.project)) {
    await page.getByRole("button", { name: `Quitar: ${fixture.project}`, exact: true }).click();
    await expect(page.getByText("Proyecto quitado.")).toBeVisible();
  }
  const found = await readProjects(page);
  try {
    // Marcador propio mínimo desde Ajustes y persistencia tras recargar.
    await page.getByLabel("Ruta de la carpeta").fill(fixture.project);
    await page.getByRole("button", { name: "Añadir", exact: true }).click();
    await expect(page.getByText("Proyecto añadido.")).toBeVisible();
    await page.reload();
    await expect(
      page.getByRole("button", { name: `Quitar: ${fixture.project}`, exact: true }),
    ).toBeVisible();
    expect(await readProjects(page)).toEqual([...found, fixture.project]);

    // Logout retira la autoridad y volver a entrar la recupera con el mismo estado.
    await page.getByRole("button", { name: "Salir" }).click();
    await expect(page.locator("#access-token")).toBeVisible();
    expect(
      (await context.request.get(new URL("/api/bootstrap", BASE!).href)).status(),
      "la autoridad fue retirada",
    ).toBe(401);
    await unlockProxy(page, fixture.token);
    await page.goto("/?section=settings");
    await expect(
      page.getByRole("button", { name: `Quitar: ${fixture.project}`, exact: true }),
    ).toBeVisible();
  } finally {
    // La shell real siempre manda Origin; sobre HTTPS el POST sin él se rechaza (CSRF).
    const response = await page.request.post("/api/settings", {
      headers: { Origin: BASE! },
      data: { projects: found.map((path) => ({ path })) },
    });
    expect([200, 201], "restaurar la lista de carpetas").toContain(response.status());
    await context.close();
  }
});

test("PL-04: Host, Origin, forwarded y CSRF ajenos no conceden autoridad ni mutan", async ({
  browser,
}) => {
  test.setTimeout(240_000);
  const fixture = requiredProxyFixture();
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await context.newPage();
  await unlockProxy(page, fixture.token);
  const bootstrap = new URL("/api/bootstrap", BASE!).href;

  // Control válido: con la condición intacta la misma petición sí pasa.
  expect((await page.request.get(bootstrap)).status()).toBe(200);

  // Condición única cambiada: origen ajeno, host ajeno y forwarded ambiguo/esquema erróneo.
  expect(
    (await page.request.get(bootstrap, { headers: { Origin: "https://attacker.example" } })).status(),
    "Origin ajeno",
  ).toBe(403);
  expect(
    (await page.request.get(bootstrap, { headers: { Host: fixture.hostileHost } })).status(),
    "Host ajeno",
  ).toBe(403);
  // El proxy normaliza X-Forwarded-Proto, así que el forwarded ambiguo se mide sobre el
  // mismo listener detrás del proxy, con la cookie propia y el Host público intactos.
  const session = (await context.cookies(BASE!)).find((row) =>
    row.name.startsWith("workbench_session_"),
  );
  const cookie = `${session!.name}=${session!.value}`;
  const behindProxy = new URL("/api/bootstrap", fixture.listener).href;
  expect(
    (await page.request.get(behindProxy, {
      headers: { Cookie: cookie, Host: new URL(BASE!).host, Origin: BASE!, "X-Forwarded-Proto": "https" },
    })).status(),
    "control directo del peer confiado",
  ).toBe(200);
  expect(
    (await page.request.get(behindProxy, {
      headers: { Cookie: cookie, Host: new URL(BASE!).host, Origin: BASE!, "X-Forwarded-Proto": "http" },
    })).status(),
    "esquema forwarded incorrecto",
  ).toBe(403);
  expect(
    (await page.request.get(behindProxy, {
      headers: { Cookie: cookie, Host: new URL(BASE!).host, Origin: BASE!, "X-Forwarded-Proto": "https, http" },
    })).status(),
    "forwarded ambiguo",
  ).toBe(403);

  // Ningún rechazo muta el registro: el POST con origen ajeno no cambia la lista.
  const before = await readProjects(page);
  const foreignWrite = await page.request.post(new URL("/api/settings", BASE!).href, {
    headers: { Origin: "https://attacker.example" },
    data: { projects: [{ path: fixture.project }] },
  });
  expect(foreignWrite.status(), "escritura con origen ajeno").toBe(403);
  expect(await readProjects(page), "la lista no cambió").toEqual(before);
  // Y un POST sin Origin tampoco (la shell siempre lo envía).
  const noOrigin = await page.request.post(new URL("/api/settings", BASE!).href, {
    data: { projects: [{ path: fixture.project }] },
  });
  expect(noOrigin.status(), "escritura sin Origin").toBe(403);
  expect(await readProjects(page), "la lista sigue igual").toEqual(before);

  // La shell sigue funcionando después de los negativos.
  await page.goto("/?section=settings");
  await expect(page.getByRole("heading", { level: 1, name: "Ajustes" })).toBeVisible();
  await context.close();
});
