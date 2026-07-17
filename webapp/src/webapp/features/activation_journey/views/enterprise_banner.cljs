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
                          "bg-white text-[--sidebar-bg] hover:bg-[--accent-2]"
                          "bg-white/10 text-white hover:bg-white/20"))}
   label])

(defn main
  "Dark enterprise upsell banner shared by the activation journey surfaces
  (feature-page headers, See Features modal footer, terminal pre-execution).

  Props:
  - :title / :subtitle  override the default copy
  - :badge-label        badge next to the title (default \"Enterprise\")
  - :primary            {:label :on-click} light action button
  - :secondary          {:label :on-click} translucent action button"
  [{:keys [title subtitle badge-label primary secondary]}]
  ;; --sidebar-bg is the app's dark navy, defined once in the React shell
  ;; theme (webapp_v2/src/theme.js) and available on every route since the
  ;; shell wraps the CLJS pages — changing the theme updates both stacks.
  [:> Box {:class "bg-[--sidebar-bg] rounded-2 px-4 py-3"}
   [:> Flex {:align "center" :justify "between" :gap "4"}
    [:> Flex {:direction "column" :gap "1"}
     [:> Flex {:align "center" :gap "2"}
      [:> Text {:size "2" :weight "bold" :class "text-white"}
       (or title default-title)]
      ;; Plain span instead of Radix Badge: this is a custom dark surface and
      ;; the themed badge colors would fight the Tailwind overrides.
      [:span {:class "rounded-sm bg-white px-1.5 py-0.5 text-xs font-medium text-[--sidebar-bg]"}
       (or badge-label "Enterprise")]]
     [:> Text {:as "p" :size "1" :class "text-white/70"}
      (or subtitle default-subtitle)]]
    (when (or primary secondary)
      [:> Flex {:gap "2" :align "center" :class "shrink-0"}
       (when secondary [banner-button secondary false])
       (when primary [banner-button primary true])])]])
