// Render the existing vector mark with Edge through Playwright (set PLAYWRIGHT_MODULE to the path of
// an installed @playwright/test). No source PNG recoloring: the SVG geometry is the canonical input.
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || '@playwright/test');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');

(async () => {
  const browser = await chromium.launch({ channel: 'msedge', headless: true });
  try {
    const page = await browser.newPage({ deviceScaleFactor: 4 });
    for (const size of [256, 64, 48, 40, 32, 24, 20, 16]) {
      const mark = readFileSync(join(__dirname, size < 32 ? 'icon-small.svg' : 'icon.svg'), 'utf8');
      const paths = mark.slice(mark.indexOf('<g '), mark.lastIndexOf('</svg>'));
      const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 128 128" fill="none"><rect width="128" height="128" rx="24" fill="#0b1522"/><g transform="translate(16 8)">${paths}</g></svg>`;
      await page.setViewportSize({ width: size, height: size });
      await page.setContent(`<html><body style="margin:0">${svg}</body></html>`);
      await page.screenshot({ path: join(__dirname, `icon-${size}.png`), omitBackground: true, scale: 'css' });
    }
  } finally { await browser.close(); }
  console.log('Blue Windows resource icons exported from SVG.');
})().catch(error => { console.error(error); process.exitCode = 1; });
