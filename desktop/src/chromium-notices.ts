// Parse Electron's generated credits in an inert template. Its HTML, scripts and
// resource links never enter the live document; only text is shown in React.
export function chromiumNoticesText(html: string): string {
  const template = document.createElement('template');
  template.innerHTML = html;
  const products = Array.from(template.content.querySelectorAll('.product'));
  if (!products.length) throw new Error('The bundled Chromium notices could not be read.');
  const sections = products.map(product => {
    const title = product.querySelector('.title')?.textContent?.trim();
    const license = product.querySelector('.license')?.textContent?.trim();
    if (!title || !license) throw new Error('The bundled Chromium notices are incomplete.');
    const homepage = product.querySelector('.homepage a')?.getAttribute('href');
    return [title, homepage, license].filter(Boolean).join('\n\n');
  });
  const source = template.content.querySelector('.open-sourced');
  const sourceText = source?.textContent?.trim().replace(/\s+/g, ' ');
  const sourceURL = source?.querySelector('a')?.getAttribute('href');
  const introduction = [sourceText, sourceURL].filter(Boolean).join('\n');
  return [introduction, ...sections].filter(Boolean).join('\n\n--------------------\n\n');
}
