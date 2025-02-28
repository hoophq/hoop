import React from 'react';

export function Button({ children, ...props }) {
  return (
    <button {...props}>
      {children}
    </button>
  );
}
