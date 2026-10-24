// Run with Node's test runner and Playwright on the module path; see README.md.
// Settings › Export in magpie web is the reader's own file: on a computer
// reached in a browser (a server), the export writes nothing here and the
// browser saves the sealed file it is handed, named as the app names it.
// The app's own windows keep the file in Downloads and the path in the
// answer. Every request keeps the passphrase in the body: never in the URL.
// No backend: the API is a fixture here.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { openPart } = require("./reader.cjs");

const assets = path.resolve(__dirname, "../assets");
const NAME = "magpie-2026-10-17.magpie-backup";
// what backup.Seal's envelope is: a JSON object, base64 in the answer
const SEALED = Buffer.from(JSON.stringify({ format: "magpie-backup", version: 1, kdf: "pbkdf2-sha256" }), "utf8").toString("base64");

function settingsPayload() {
  return {
    theme: "light", lang: "en", tray: "panel", quotaLeft: false, currency: "usd",
    dock: false, dockWindow: false, proxy: "", redact: false, redactPersonal: false, redactWords: [],
    codexWarmup: "", claudeWarmup: "", codexWarmAt: "", claudeWarmAt: "", workbuddyCheckin: false, noStats: false,
    trayUsage: "", trayUsages: [], trayUsageEvery: 3, vision: "", imageGen: "",
    version: "0.1.400", dir: "~/.config/magpie", gateway: "http://127.0.0.1:3425",
    proxyNow: "none", proxySource: "none", login: false,
    visionModels: [], imageGenModels: [], workbuddyCheckins: [], lanURLs: [],
    fx: { rate: 7.2, at: new Date().toISOString(), stale: false },
  };
}

function server(web, posts) {
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"en",theme:"light",web:${web}};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: { lang: "en", theme: "light" } });
    if (url.pathname === "/api/settings") return json(settingsPayload());
    if (url.pathname === "/api/davsync") return json({ servers: {}, error: "" });
    if (url.pathname === "/api/backup/export") {
      posts.push({ url: req.url(), body: req.postDataJSON() });
      // the two answers the route has: the browser is handed the file, the
      // app is told the path it wrote
      return json(web
        ? { name: NAME, data: SEALED, providers: 0, profiles: 0, agents: 0, library: true }
        : { path: "~/Downloads/" + NAME, providers: 0, profiles: 0, agents: 0, library: true });
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  test(engine + ": the export goes where the page is read", async (t) => {
    assert(["chromium", "webkit"].includes(engine), "BROWSER must be chromium or webkit");
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    const pages = [];
    t.after(async () => {
      if (process.env.ARTIFACT_DIR) {
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        for (const [i, p] of pages.entries()) await p.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-backup-export-${i}.png`) });
      }
      await browser.close();
    });

    for (const web of [true, false]) {
      await t.test(web ? "magpie web: the browser saves it" : "the app: Downloads", async () => {
        const posts = [], downloads = [];
        const context = await browser.newContext({ viewport: { width: 1100, height: 700 }, reducedMotion: "reduce" });
        const page = await context.newPage();
        pages.push(page);
        page.setDefaultTimeout(5000);
        page.on("download", (d) => downloads.push(d));
        await page.route("**/*", server(web, posts));
        await page.goto("http://magpie.test/?view=settings");
        await page.locator("#prefs").click().catch(() => {});
        await openPart(page, "sync");
        await page.getByRole("button", { name: "Export…" }).click();
        const form = page.locator("#syncList .sync-form");
        const fields = form.locator("input[type=password]");
        await fields.nth(0).fill("pw");
        await fields.nth(1).fill("pw");
        const waiting = web ? page.waitForEvent("download") : null;
        await form.getByRole("button", { name: "Export", exact: true }).click();
        const done = page.locator("#syncList .done");
        await done.waitFor();

        assert.equal(posts.length, 1, "one export request");
        assert.equal(posts[0].body.pass, "pw", "the passphrase goes in the body");
        assert(!posts[0].url.includes("pw"), "the passphrase is never in the URL: " + posts[0].url);

        if (web) {
          const dl = await waiting;
          assert.equal(dl.suggestedFilename(), NAME, "the browser saves it under the app's own name");
          const bytes = await fs.readFile(await dl.path());
          assert.equal(JSON.parse(bytes.toString()).format, "magpie-backup", "the file is the sealed bundle");
          assert.deepEqual(await page.locator("#syncList").innerText().then((s) => s.includes("~/")), false, "no path on a computer the reader can't reach");
          assert.equal((await done.innerText()).trim(), "Saved to " + NAME, "the page names the file saved");
        } else {
          await page.waitForTimeout(300);
          assert.equal(downloads.length, 0, "the app's window downloads nothing");
          assert.equal((await done.innerText()).trim(), "Saved to ~/Downloads/" + NAME, "the page names the file on this computer");
        }
        await context.close();
      });
    }
  });
}
