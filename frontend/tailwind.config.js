/** @type {import('tailwindcss').Config} */
export default {
	content: ['./src/**/*.{html,js,svelte,ts}'],
	theme: {
		extend: {
			fontFamily: {
				sans: ['Archivo', 'ui-sans-serif', 'system-ui', 'sans-serif']
			},
			colors: {
				bastion: {
					50: '#f0f9ff',
					100: '#e0f2fe',
					500: '#0ea5e9',
					600: '#0284c7',
					700: '#0369a1',
					900: '#0c4a6e'
				}
			}
		}
	},
	plugins: []
};
