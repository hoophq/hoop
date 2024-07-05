(ns webapp.components.tabs
  (:require
   [reagent.core :as r]))

(defn- list-item [item on-click]
  [:a
   {:key (:name item)
    :on-click #(on-click (:name item))
    :class (str (if (:current item)
                  "border-gray-900 text-gray-900"
                  "border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-200 cursor-pointer")
                " whitespace-nowrap flex py-regular px-small border-b-2 font-medium text-sm")
    :role "tab"
    :aria-current (if (:current item) "page" nil)}
   (:name item)])

(defn- state-builder [item default-value]
  {item {:name item :current (if (= item default-value) true false)}})

(defn- update-tab-view [tabs-list clicked-tab]
  (doall (map
          (fn [[key _]]
            (if (= key clicked-tab)
              (swap! tabs-list assoc-in [key :current] true)
              (swap! tabs-list assoc-in [key :current] false)))
          @tabs-list)))

(defn tabs
  "on-change -> a lambda that will return the value clicked
   default-value -> a name from the tabs list that has to be the default selected item. Default is the first item from tabs
   tabs -> a list of symbols or strings for each tab to be shown

  ;;;;;;;;;;;
  ;; USAGE ;;
  ;;;;;;;;;;;

  [tabs/tabs
    {:on-change #(println %) -> you can use this to manage a panel view
     :tabs [:Sessions :Reviews] -> inside the tabs it mounts a complex state, no need to worry about it in your implementation
     :default-value :Sessions}] -> optional, if not provided, it gets the first value from :tabs configuration
  "
  [config]
  (let [default-value (or (:default-value config) (first (:tabs config)))
        ;; tabs-state model example
        ;; {tab {:name tab :current true} tab {:name tab :current false}}
        tabs-state (r/atom (into
                            {}
                            (mapv #(state-builder % default-value)
                                  (:tabs config))))
        on-change (fn [clicked-tab]
                    ((:on-change config) clicked-tab)
                    (update-tab-view tabs-state clicked-tab))]
    (fn [_]
      [:div.mb-large
       [:div.sm:block
        [:div.border-b.border-gray-200
         [:nav.-mb-px.flex.space-x-8
          {:aria-label :Tabs}
          (doall (map
                  (fn [[_ value]]
                    ^{:key value}
                    [list-item value on-change])
                  @tabs-state))]]]])))
