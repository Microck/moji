// Exercise the valid extensionless response that broke Next.js before 16.4.3.
module.exports = {
  'https://fonts.googleapis.com/css2?family=Google+Sans+Flex:opsz,wght@6..144,1..1000&display=swap': `
/* latin */
@font-face {
  font-family: 'Google Sans Flex';
  font-style: normal;
  font-weight: 1 1000;
  font-display: swap;
  src: url(${process.env.MOJI_TEST_FONT_ORIGIN}/l/font?kit=a%2Bb%25c=d&skey=test&v=v20) format('woff2');
}
`,
};
