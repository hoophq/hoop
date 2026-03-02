(ns webapp.webclient.log-area.output-tabs)

(defn- list-item [key value selected-tab on-click is-first? tabs-vec]
  (let [aria-label (case key
                     :logs "Logs output"
                     :tabular "Tabular output"
                     value)]
    [:a
     {:key key
      :on-click #(on-click key value)
      :class (str (when (= value selected-tab) "border-b border-[--gray-a6] ")
                  "uppercase cursor-pointer text-[--gray-a11]"
                  " whitespace-nowrap font-medium text-xxs"
                  " py-small px-small text-center")
      :role "tab"
      :aria-label aria-label
      :aria-selected (if (= value selected-tab) "true" "false")
      :aria-controls (str "tabpanel-" key)
      :tabIndex (if (= value selected-tab) "0" "-1")
      :data-focus-after-editor (when is-first? true)
      :on-key-down (fn [e]
                     (case (.-key e)
                       "ArrowRight" (let [current-idx (.indexOf tabs-vec [key value])
                                          next-idx (mod (inc current-idx) (count tabs-vec))
                                          [next-key next-value] (nth tabs-vec next-idx)]
                                      (.preventDefault e)
                                      (on-click next-key next-value)
                                      (js/setTimeout #(when-let [elem (.querySelector js/document (str "[aria-controls='tabpanel-" next-key "']"))]
                                                        (.focus elem)) 0))
                       "ArrowLeft" (let [current-idx (.indexOf tabs-vec [key value])
                                         prev-idx (mod (dec current-idx) (count tabs-vec))
                                         [prev-key prev-value] (nth tabs-vec prev-idx)]
                                     (.preventDefault e)
                                     (on-click prev-key prev-value)
                                     (js/setTimeout #(when-let [elem (.querySelector js/document (str "[aria-controls='tabpanel-" prev-key "']"))]
                                                       (.focus elem)) 0))
                       nil))}
     value]))

(defn tabs
  "[tabs/tabs
    {:on-click #(println %1 %2) ;; returns the key (%1) and value (%2) for the clicked tab
     :tabs {:tab-value \"Tab text\"}
     :selected-tab :tab-value}]
  "
  [{:keys [on-click selected-tab tabs]}]
  (let [tabs-vec (vec tabs)]
    [:div {:class "mb-regular"}
     [:div {:class "sm:block"}
      [:div
       [:nav {:class "-mb-px flex gap-small"
              :aria-label "Output tabs"
              :role "tablist"}
        (doall (map-indexed
                (fn [idx [key value]]
                  ^{:key key}
                  [list-item key value selected-tab on-click (zero? idx) tabs-vec])
                tabs))]]]]))

