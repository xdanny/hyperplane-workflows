import { defineConfig } from 'vite';

// Serve the production files only. Unlike the application Vite configuration,
// this test preview has no API/file proxy and cannot reach the live backend.
export default defineConfig({ preview: { proxy: {} } });
