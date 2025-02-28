// This is a bridge between React components and re-frame
declare global {
  interface Window {
    // re-frame dispatch function
    re_frame?: {
      core: {
        dispatch: (event: any[]) => void;
        dispatch_sync: (event: any[]) => void;
        subscribe: (subscription: any[]) => { 
          deref: () => any;
          destroy: () => void;
        };
      };
    };
    // ClojureScript core namespace
    cljs?: {
      core: {
        Vector: any;
        vector: (...args: any[]) => any;
        keyword: (str: string) => any;
      };
    };
  }
}

function toClojure(value: any): any {
  if (typeof value === 'string' && value.startsWith(':')) {
    return window.cljs?.core.keyword(value.slice(1));
  }
  return value;
}

export function dispatch(event: any[]) {
  const args = event.map(toClojure);
  const vector = window.cljs?.core.vector(...args);
  if (vector) {
    window.re_frame?.core.dispatch(vector);
  }
}

export function dispatchSync(event: any[]) {
  const args = event.map(toClojure);
  const vector = window.cljs?.core.vector(...args);
  if (vector) {
    window.re_frame?.core.dispatch_sync(vector);
  }
}

export function subscribe(subscription: any[]) {
  const args = subscription.map(toClojure);
  const vector = window.cljs?.core.vector(...args);
  if (vector) {
    return window.re_frame?.core.subscribe(vector);
  }
  return undefined;
}

// Hook to use re-frame subscriptions in React components
import { useEffect, useState } from 'react';

export function useSubscription<T>(subscription: any[]): T | undefined {
  const [value, setValue] = useState<T>();

  useEffect(() => {
    const args = subscription.map(toClojure);
    const vector = window.cljs?.core.vector(...args);
    const sub = vector ? window.re_frame?.core.subscribe(vector) : undefined;
    
    if (sub) {
      // Initial value
      setValue(sub.deref());

      // Setup watcher
      const interval = setInterval(() => {
        setValue(sub.deref());
      }, 100); // Poll every 100ms

      return () => {
        clearInterval(interval);
        sub.destroy();
      };
    }
  }, [subscription.join(',')]);

  return value;
} 
