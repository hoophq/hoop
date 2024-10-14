/** @type {import('tailwindcss').Config} */
const plugin = require("tailwindcss/plugin");
const { fontFamily } = require("tailwindcss/defaultTheme")

module.exports = {
  content: ["./src/webapp/**/*.cljs", "./resource/public/js/cljs-runtime/*.js", "./node_modules/react-tailwindcss-datepicker/dist/index.esm.js"],
  darkMode: ["class"],
  screens: {
    'sm': '640px',
    // => @media (min-width: 640px) { ... }

    'md': '768px',
    // => @media (min-width: 768px) { ... }

    'lg': '1024px',
    // => @media (min-width: 1024px) { ... }

    'xl': '1280px',
    // => @media (min-width: 1280px) { ... }

    '2xl': '1536px',
    // => @media (min-width: 1536px) { ... }
  },
  theme: {
    extend: {
      fontSize: {
        xxs: ["0.625rem", "0.85rem"],
      },
      transitionProperty: {
        height: "height",
        spacing: "margin, padding",
      },
      colors: {
        gray: {
          1: 'var(--gray-1)',
          2: 'var(--gray-2)',
          3: 'var(--gray-3)',
          4: 'var(--gray-4)',
          5: 'var(--gray-5)',
          6: 'var(--gray-6)',
          7: 'var(--gray-7)',
          8: 'var(--gray-8)',
          9: 'var(--gray-9)',
          10: 'var(--gray-10)',
          11: 'var(--gray-11)',
          12: 'var(--gray-12)',
        },
        warning: {
          1: 'var(--amber-1)',
          2: 'var(--amber-2)',
          3: 'var(--amber-3)',
          4: 'var(--amber-4)',
          5: 'var(--amber-5)',
          6: 'var(--amber-6)',
          7: 'var(--amber-7)',
          8: 'var(--amber-8)',
          9: 'var(--amber-9)',
          10: 'var(--amber-10)',
          11: 'var(--amber-11)',
          12: 'var(--amber-12)',
        },
        success: {
          1: 'var(--green-1)',
          2: 'var(--green-2)',
          3: 'var(--green-3)',
          4: 'var(--green-4)',
          5: 'var(--green-5)',
          6: 'var(--green-6)',
          7: 'var(--green-7)',
          8: 'var(--green-8)',
          9: 'var(--green-9)',
          10: 'var(--green-10)',
          11: 'var(--green-11)',
          12: 'var(--green-12)',
        },
        info: {
          1: 'var(--sky-1)',
          2: 'var(--sky-2)',
          3: 'var(--sky-3)',
          4: 'var(--sky-4)',
          5: 'var(--sky-5)',
          6: 'var(--sky-6)',
          7: 'var(--sky-7)',
          8: 'var(--sky-8)',
          9: 'var(--sky-9)',
          10: 'var(--sky-10)',
          11: 'var(--sky-11)',
          12: 'var(--sky-12)',
        },
        error: {
          1: 'var(--red-1)',
          2: 'var(--red-2)',
          3: 'var(--red-3)',
          4: 'var(--red-4)',
          5: 'var(--red-5)',
          6: 'var(--red-6)',
          7: 'var(--red-7)',
          8: 'var(--red-8)',
          9: 'var(--red-9)',
          10: 'var(--red-10)',
          11: 'var(--red-11)',
          12: 'var(--red-12)',
        },
        border: "hsl(var(--border))",
        input: "hsl(var(--input))",
        ring: "hsl(var(--ring))",
        background: "hsl(var(--background))",
        foreground: "hsl(var(--foreground))",
        primary: {
          DEFAULT: "hsl(var(--primary))",
          foreground: "hsl(var(--primary-foreground))",
          1: 'var(--indigo-1)',
          2: 'var(--indigo-2)',
          3: 'var(--indigo-3)',
          4: 'var(--indigo-4)',
          5: 'var(--indigo-5)',
          6: 'var(--indigo-6)',
          7: 'var(--indigo-7)',
          8: 'var(--indigo-8)',
          9: 'var(--indigo-9)',
          10: 'var(--indigo-10)',
          11: 'var(--indigo-11)',
          12: 'var(--indigo-12)',
        },
        secondary: {
          DEFAULT: "hsl(var(--secondary))",
          foreground: "hsl(var(--secondary-foreground))",
        },
        destructive: {
          DEFAULT: "hsl(var(--destructive))",
          foreground: "hsl(var(--destructive-foreground))",
        },
        muted: {
          DEFAULT: "hsl(var(--muted))",
          foreground: "hsl(var(--muted-foreground))",
        },
        accent: {
          DEFAULT: "hsl(var(--accent))",
          foreground: "hsl(var(--accent-foreground))",
        },
        popover: {
          DEFAULT: "hsl(var(--popover))",
          foreground: "hsl(var(--popover-foreground))",
        },
        card: {
          DEFAULT: "hsl(var(--card))",
          foreground: "hsl(var(--card-foreground))",
        },
      },
      height: {
        "sessions-list": "calc(100vh - 160px)",
        "audit-sessions-list": "calc(100vh - 245px)",
        "connections-list": "calc(100vh - 160px)",
        "plugins-list": "calc(100vh - 160px)",
        "new-task__screen-container": "calc(100vh - 68px)",
        "templates__screen-container": "calc(100vh - 140px)",
        "reviews__screen-container": "calc(100vh - 68px)",
        "reviews-plugin__screen-container": "calc(100vh - 140px)",
        "new-task__tree-container": "calc(100vh - 140px)",
        "screen-90vh": "90vh",
        "logs-container": "calc(100% - 38px)",
        "connection-selector": "calc(100vh - 56px)",
      },
      width: {
        "side-menu": "296px",
        "floating-search-webclient": "calc(100% - 50px)",
      },
      minWidth: {
        64: "16rem",
        app__container: "980px",
      },
      keyframes: {
        "appear-right": {
          "0%": {
            transform: "translateX(100px)",
            opacity: 0,
          },
          "100%": {
            transform: "translateX(0)",
            opacity: 100,
          },
        },
        "accordion-down": {
            from: { height: "0" },
            to: { height: "var(--radix-accordion-content-height)" },
          },
          "accordion-up": {
           from: { height: "var(--radix-accordion-content-height)" },
            to: { height: "0" },
          },
      },
      animation: {
        "accordion-down": "accordion-down 0.2s ease-out",
        "accordion-up": "accordion-up 0.2s ease-out",
      },
      borderRadius: {
        DEFAULT: "2px",
        t: "2px 2px 0 0",
        "t-lg": "4px 4px 0 0",
        "l-lg": "4px 0 0 4px",
        "r-lg": "0 4px 4px 0",
        full: "9999px",
        b: "0 0 2px 2px",
        lg: `var(--radius)`,
        md: `calc(var(--radius) - 2px)`,
        sm: "calc(var(--radius) - 4px)",
        1: "4.5px",
        2: "6px",
        3: "9px",
        4: "12px",
        5: "18px",
        6: "24px",
      },
      boxShadow: {
        "red-button-hover": "4px 4px #ed5a5a",
        "black-button-hover": "4px 4px #777",
        "secondary-button-hover": "4px 4px #dbdbdb",
        "blue-button-hover":
          "4px 4px rgba(147, 197, 253, var(--tw-text-opacity))",
      },
      textColor: {
        magenta: "#ff29ff",
      },
      animation: { "appear-right": "appear-right .15s ease-in-out" },
      spacing: {
        "radix-1": "var(--space-1)",
        "radix-2": "var(--space-2)",
        "radix-3": "var(--space-3)",
        "radix-4": "var(--space-4)",
        "radix-5": "var(--space-5)",
        "radix-6": "var(--space-6)",
        "radix-7": "var(--space-7)",
        "radix-8": "var(--space-8)",
        "radix-9": "var(--space-9)",
        "x-small": ".25rem",
        small: ".5rem",
        regular: "1rem",
        large: "2rem",
        "x-large": "4rem",
        "side-menu-width": "296px",
      },
      left: {
        "side-menu-width": "296px",
      },
      backgroundColor: {
        editor: "#232834",
      },
      backgroundImage: {
        "auth-cover":
          "url('https://images.unsplash.com/photo-1518937580590-43e63e8b96ca?ixid=MnwxMjA3fDB8MHxwaG90by1wYWdlfHx8fGVufDB8fHx8&ixlib=rb-1.2.1&auto=format&fit=crop&w=1350&q=80')",
      },
      cursor: {
        "ns-resize": "ns-resize",
        "ew-resize": "ew-resize",
      },
    },
  },
  plugins: [
    require("tailwindcss-animate"),
    plugin(function ({ addUtilities }) {
      const rotateY = {
        ".rotate-y-180": {
          transform: "rotateY(180deg)",
        },
      };

      const backfaceVisibility = {
        ".backface-visibility-hidden": {
          "backface-visibility": "hidden",
        },
      };

      addUtilities(backfaceVisibility, []);
      addUtilities(rotateY, ["group-hover", "hover"]);
    }),
  ],
};
