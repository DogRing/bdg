import puppeteer from 'puppeteer';

(async () => {
  const browser = await puppeteer.launch({ args: ['--no-sandbox', '--disable-setuid-sandbox'] });
  const page = await browser.newPage();
  
  // Set viewport to a good size for viewing the canvas + sidebar
  await page.setViewport({ width: 1280, height: 720 });
  
  await page.goto('http://localhost:3000', { waitUntil: 'networkidle0' });
  
  // Wait a bit for SSE to populate
  await new Promise(resolve => setTimeout(resolve, 3000));
  
  await page.screenshot({ path: 'frontend_test.png' });
  await browser.close();
  console.log('Screenshot saved to frontend_test.png');
})();
