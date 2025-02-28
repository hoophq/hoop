// Re-export the ClojureScript config values
declare global {
  interface Window {
    config?: {
      webappUrl: string;
      [key: string]: any;
    };
  }
}

export const config = {
  webappUrl: window.config?.webappUrl || '',
  // Add other configuration values as needed
} as const; 
