(ns webapp.webclient.log-area.output-tabs)

(defn- list-item [key value selected-tab on-click]
  [:a
   {:key key
    :on-click #(on-click key value)
    :class (str (when (= value selected-tab) "border-b border-[--gray-a6] ")
                "uppercase cursor-pointer text-[--gray-a11]"
                " whitespace-nowrap font-medium text-xxs"
                " py-small px-small text-center")
    :role "tab"
    :aria-current (if (= value selected-tab) "page" nil)}
   value])

(defn tabs
  "[tabs/tabs
    {:on-click #(println %1 %2) ;; returns the key (%1) and value (%2) for the clicked tab
     :tabs {:tab-value \"Tab text\"}
     :selected-tab :tab-value}]
  "
  [{:keys [on-click selected-tab tabs]}]
  [:div {:class "mb-regular"}
   [:div {:class "sm:block"}
    [:div
     [:nav {:class "-mb-px flex gap-small"
            :aria-label :Tabs}
      (doall (map
              (fn [[key value]]
                ^{:key key}
                [list-item key value selected-tab on-click])
              tabs))]]]])
