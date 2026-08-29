/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        surface: {
          DEFAULT: '#0f1117',
          raised: '#1a1d27',
          overlay: '#242835',
        },
        accent: {
          DEFAULT: '#7c6cf6',
          hover: '#6a5ae5',
        },
      },
    },
  },
  plugins: [],
}
