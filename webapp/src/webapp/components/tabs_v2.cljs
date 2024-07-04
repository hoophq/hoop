(ns webapp.components.tabs-v2
  (:require
   [reagent.core :as r]))

(defn- list-item [key value selected-tab on-click]
  [:a
   {:key key
    :on-click #(on-click key value)
    :class (str (if (= key selected-tab)
                  (str "border-gray-400 bg-gray-100 text-gray-900")
                  (str "cursor-pointer border-gray-300 text-gray-700"
                       " hover:bg-gray-100"))
                " flex-grow rounded-lg border transition"
                " whitespace-nowrap font-medium text-sm"
                " py-small px-small text-center")
    :role "tab"
    :aria-current (if (= value selected-tab) "page" nil)}
   value])

(defn tabs
  "[tabs/tabs
    {:on-click #(println %1 %2) ;; returns the key (%1) and value (%2) for the clicked tab
     :tabs {:tab-value \"Tab text\"}
     :value :tab-value}]
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
