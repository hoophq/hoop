const defaultTheme = require("tailwindcss/defaultTheme");
const plugin = require("tailwindcss/plugin");

module.exports = {
  purge: {
    content: ["./**/*.js", "./**/*.html"],
  },
  darkMode: false,
  theme: {
    ...defaultTheme,
    extend: {
      fontFamily: {
        sans: ["Sora", "sans-serif", ...defaultTheme.fontFamily.sans],
        mono: ["Inconsolata", ...defaultTheme.fontFamily.mono],
      },
      fontSize: {
        xxs: ["0.625rem", "0.85rem"],
      },
      transitionProperty: {
        height: "height",
        spacing: "margin, padding",
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
        "terminal-container": "calc(100% - 38px)",
        "connection-selector": "calc(100vh - 56px)",
      },
      width: {
        "side-menu": "296px",
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
      },
      borderRadius: {
        DEFAULT: "2px",
        lg: "8px",
        t: "2px 2px 0 0",
        "t-lg": "4px 4px 0 0",
        "l-lg": "4px 0 0 4px",
        "r-lg": "0 4px 4px 0",
        full: "9999px",
        md: "0.375rem",
        b: "0 0 2px 2px",
      },
      borderColor: {
        "t-gray-600": `${defaultTheme.colors.gray["600"]} currentColor currentColor`,
        "t-gray-400": `${defaultTheme.colors.gray["400"]} currentColor currentColor`,
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
  variants: {
    opacity: ({ after }) => after(["disabled"]),
    extend: {
      padding: ["hover", "last", "first"],
      shadow: ["hover"],
      SVGFEDisplacementMapElementy: ["group-hover", "hover"],
      transform: ["group-hover", "hover"],
      translate: ["group-hover", "hover"],
      rotate: ["group-hover", "hover"],
      borderWidth: ["last", "first", "group-hover", "hover"],
      display: ["group-hover"],
    },
  },
  plugins: [
    require("@tailwindcss/forms"),
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
