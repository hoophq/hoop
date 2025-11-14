(ns webapp.components.infinite-scroll
  (:require [reagent.core :as r]))

(defn infinite-scroll
  [{:keys [on-load-more has-more? loading?]}
   & children]
  (r/with-let [params (atom {:on-load-more on-load-more
                             :has-more? has-more?
                             :loading? loading?})
               observer (atom nil)]
    (reset! params {:on-load-more on-load-more
                    :has-more? has-more?
                    :loading? loading?})
    (when-not @observer
      (let [callback (fn [entries _]
                       (let [{:keys [on-load-more has-more? loading?]} @params]
                         (when (and (seq entries)
                                    (.-isIntersecting (first entries))
                                    has-more?
                                    (not loading?)
                                    on-load-more)
                           (on-load-more))))]
        (reset! observer (js/IntersectionObserver. callback #js {:threshold 1.0}))))
    [:<>
     children
     [:div {:ref (fn [el]
                   (when (and el (not= el (aget @observer "_observedEl")))
                     (when-let [obs @observer]
                       (when (aget obs "_observedEl")
                         (.unobserve obs (aget obs "_observedEl")))
                       (aset obs "_observedEl" el)
                       (.observe obs el))))
            :style {:height "1px"}}]]
    (finally
      (when-let [obs @observer]
        (.disconnect obs)))))
