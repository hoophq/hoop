(ns webapp.features.rulepacks.main
  (:require
   ["@radix-ui/themes" :refer [Box Flex]]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.loaders :as loaders]
   [webapp.features.rulepacks.views.detail :as detail]
   [webapp.features.rulepacks.views.list :as list-view]))

(defn- with-flag-gate [component]
  ;; Ensures the experimental.rulepacks flag is loaded before rendering. If the
  ;; flag exists but is disabled, redirects to home. If it's loaded and enabled,
  ;; renders the wrapped component.
  (rf/dispatch [:settings-experimental/get-flags])
  (let [redirected? (r/atom false)]
    (fn [component]
      (let [status @(rf/subscribe [:settings-experimental/status])
            enabled? @(rf/subscribe [:rulepacks/enabled?])]
        (cond
          (= :loading status)
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]]

          (and (= :success status) (not enabled?))
          (do
            (when (not @redirected?)
              (reset! redirected? true)
              (js/setTimeout #(rf/dispatch [:navigate :home]) 800))
            [:> Box {:class "bg-gray-1 h-full"}
             [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
              [loaders/simple-loader]]])

          enabled?
          component

          :else
          [:> Box {:class "bg-gray-1 h-full"}
           [:> Flex {:direction "column" :justify "center" :align "center" :height "100%"}
            [loaders/simple-loader]]])))))

(defn list-page []
  [with-flag-gate [list-view/main]])

(defn detail-page [{:keys [rulepack-id]}]
  [with-flag-gate [detail/main {:rulepack-id rulepack-id}]])
