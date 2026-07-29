(ns webapp.features.activation-journey.views.enterprise-banner
  (:require
   ["@radix-ui/themes" :refer [Box Flex Text]]))

(def default-title "Unlock all protection controls")
(def default-subtitle "Unlock unlimited Guardrails, Masking Rules, AI Session Analyzer, and more.")

(defn- banner-button [{:keys [label on-click]} primary?]
  [:button {:type "button"
            :on-click on-click
            :class (str "shrink-0 rounded-md px-3 py-1.5 text-sm font-medium transition-colors "
                        (if primary?
                          "bg-white text-[var(--brand-navy,#1F2D5C)] hover:bg-[--accent-2]"
                          "bg-white/10 text-white hover:bg-white/20"))}
   label])

(defn main
  "Dark enterprise upsell banner shared by the activation journey surfaces
  (feature-page headers, See Features modal footer, terminal pre-execution).

  Props:
  - :title / :subtitle  override the default copy
  - :badge-label        badge next to the title (default \"Enterprise\")
  - :primary            {:label :on-click} light action button
  - :secondary          {:label :on-click} translucent action button
  - :flat?              square corners, for banners attached to another
                        surface (e.g. glued under the terminal tabs). The
                        default rounded card stays for standalone placements."
  [{:keys [title subtitle badge-label primary secondary flat?]}]
  ;; --brand-navy is the brand's dark navy (the old sidemenu blue), defined
  ;; once in the React shell theme (webapp_v2/src/theme.js) and available on
  ;; every route since the shell wraps the CLJS pages — changing the theme
  ;; updates both stacks. The literal fallback keeps the banner dark if the
  ;; CLJS bundle ever renders outside the shell.
  [:> Box {:class (str "bg-[var(--brand-navy,#1F2D5C)] px-4 py-3"
                       (when-not flat? " rounded-2"))}
   [:> Flex {:align "center" :justify "between" :gap "4"}
    [:> Flex {:direction "column" :gap "1"}
     [:> Flex {:align "center" :gap "2"}
      [:> Text {:size "2" :weight "bold" :class "text-white"}
       (or title default-title)]
      ;; Plain span instead of Radix Badge: this is a custom dark surface and
      ;; the themed badge colors would fight the Tailwind overrides.
      [:span {:class "rounded-sm bg-white px-1.5 py-0.5 text-xs font-medium text-[var(--brand-navy,#1F2D5C)]"}
       (or badge-label "Enterprise")]]
     [:> Text {:as "p" :size "1" :class "text-white/70"}
      (or subtitle default-subtitle)]]
    (when (or primary secondary)
      [:> Flex {:gap "2" :align "center" :class "shrink-0"}
       (when secondary [banner-button secondary false])
       (when primary [banner-button primary true])])]])
