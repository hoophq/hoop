(ns webapp.features.activation-journey.views.enterprise-banner
  (:require
   ["@radix-ui/themes" :refer [Badge Box Flex Text]]))

(def default-title "Unlock all protection controls")
(def default-subtitle "Unlock unlimited Guardrails, Masking Rules, AI Session Analyzer, and more.")

(defn- banner-button [{:keys [label on-click]} primary?]
  [:button {:type "button"
            :on-click on-click
            :class (str "shrink-0 rounded-md px-3 py-1.5 text-sm font-medium transition-colors "
                        (if primary?
                          "bg-[--gray-1] text-[--gray-12] hover:bg-[--gray-4]"
                          "bg-white/10 text-[--gray-1] hover:bg-white/20"))}
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
  [:> Box {:class "bg-[--gray-12] rounded-2 px-4 py-3"}
   [:> Flex {:align "center" :justify "between" :gap "4"}
    [:> Box
     [:> Flex {:align "center" :gap "2"}
      [:> Text {:size "2" :weight "bold" :class "text-[--gray-1]"}
       (or title default-title)]
      [:> Badge {:size "1" :variant "soft" :color "gray" :highContrast true
                 :class "bg-white/10 text-[--gray-1]"}
       (or badge-label "Enterprise")]]
     [:> Text {:as "p" :size "1" :class "text-[--gray-8]"}
      (or subtitle default-subtitle)]]
    (when (or primary secondary)
      [:> Flex {:gap "2" :align "center" :class "shrink-0"}
       (when secondary [banner-button secondary false])
       (when primary [banner-button primary true])])]])
