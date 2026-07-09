# How People Obtain Paid Fonts for Free — Reddit Research

Compiled from r/Piracy, r/typography, and r/web_design threads.

Primary sources:
- https://www.reddit.com/r/Piracy/comments/8tqfg6/how_to_download_paid_fonts_for_free/ (1,450+ upvotes)
- https://www.reddit.com/r/Piracy/comments/180lpr2/piracy_of_fonts/
- https://pixelambacht.nl/2017/github-font-piracy/
- r/Piracy megathread (fonts section under Software)

---

## Method 1: Google Dorking (most popular)

The dominant method. Works because developers accidentally commit commercial fonts to public repos.

Standard queries:
```
"FontName".ttf
"FontName".otf
```

Variants to cast a wider net:
```
site:github.com "FontName".ttf
intitle:"FontName".ttf github
site:vk.com "FontName".ttf
"FontName" indexof:.ttf
```

Why it works: A 2017 analysis (pixelambacht.nl) found:
- 100,000+ copies of Helvetica on GitHub
- 25% of MyFonts' entire catalog (7,617 of 29,951 fonts) findable on GitHub
- 316,358 unique repos contained at least one commercial font
- Top pirated fonts: Helvetica (100k), Proxima Nova (68k), Myriad Pro (39k), Avenir (32k), Museo (32k)

Most of these uploads are accidental — developers `git add` their entire project folder without realizing the font files are licensed.

## Method 2: Dedicated Font Aggregator Sites

- **getthefont.com** — searches all GitHub repos for font files (use http:// not https://)
- **getfonts.cc** — another aggregator
- **printroot.com** — community where you can request fonts, usually filled within 3 days
- **freedafonts.com** — rehosts font files, ranks well on Google
- **DaFont / FontSpace** — large free font libraries, but criticized for low quality, incomplete character sets, and occasionally corrupt files
- r/Piracy megathread has a curated fonts section under "Software" with direct-download sites

## Method 3: Extracting from Websites (DevTools)

For fonts loaded on live websites (brands, foundry preview pages, etc.):

1. Open browser DevTools
2. Go to Network tab
3. Reload the page
4. Filter for `.woff2` / `.woff` / `.ttf` files
5. Download directly from the network request list

Tools that automate this:
- **Font Extractor** (Chrome extension) — downloads font files from the current page
- **WhatFont** — hover-to-identify, shows font family being used
- **Font Ninja** — identifies fonts on websites, helps locate them
- **font-stealer.vercel.app** — URL-based extraction
- **infyways.com/tools/font-extractor/** — URL-based extraction

Quote from Reddit: "I go on websites like Adidas, click 'edit HTML' and Ctrl+F search for .ttf"

## Method 4: Extracting from PDFs

If you have a PDF that uses a commercial font:

- **FontForge** (open source) — File → Open → set filter to "Extract from PDF" → select font
- **mupdf** — `mutool extract file.pdf` pulls embedded fonts
- **fontforce** — font extraction tool
- **ExtractPDF.com** — web-based extraction

Useful for recovering fonts from old documents where the license was lost.

## Method 5: Font Identification + Lookup

Workflow when you don't know the font name:
1. Use **WhatTheFont** (MyFonts), **Font Squirrel Matcherator**, or **WhatFontIs** to identify a font from an image
2. Then use Method 1 (Google dorking) to find the file
- **en.likefont.com** also works for identification

## Method 6: Ripping from Outlined Art

Manual method used by graphic designers:
1. Take a screenshot of the font rendered on a foundry preview page
2. Import into Illustrator
3. Use Live Trace to auto-vectorize the glyphs (~85% success rate)
4. Reassemble into a working font file via FontForge

## Legal Context

- **US**: The typeface *design* is not copyrightable, but the font *file* (the software) is. Distributing .ttf/.otf files is the violation.
- **Outlining workaround**: Converting text to outlines before sending to print converts the font to vector shapes, removing the original font file from the equation. Gray area.
- **EU**: The typeface design itself is protected as a work of art — outlining may not protect you.
- Companies do get sued. HADOPI (France's anti-piracy agency) was famously caught using an unlicensed font in their own logo.
- Web usage is the riskiest — embedded fonts are trivial to download and easy to detect/prove.
