(ns webapp.components.tooltip
  (:require ["@radix-ui/themes" :refer [Tooltip]]
            [reagent.core :as r]))

(defn truncate-tooltip [{:keys [text]}]
  (let [text-ref (r/atom nil)
        is-truncated? (r/atom false)]
    (r/create-class
     {:component-did-mount
      (fn []
        (when-let [element @text-ref]
          (reset! is-truncated? (> (.-scrollWidth element) (.-clientWidth element)))))

      :component-did-update
      (fn [this [_ old-props] [_ new-props]]
        (when (not= (:text old-props) (:text new-props))
          (when-let [element @text-ref]
            (reset! is-truncated? (> (.-scrollWidth element) (.-clientWidth element))))))

      :reagent-render
      (fn [{:keys [text]}]
        (if @is-truncated?
          [:> Tooltip {:content text}
           [:span {:ref #(reset! text-ref %)
                   :class "inline-block truncate w-full"}
            text]]
          [:span {:ref #(reset! text-ref %)
                  :class "inline-block truncate w-full"}
           text]))})))
