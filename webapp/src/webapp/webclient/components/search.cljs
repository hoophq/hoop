(ns webapp.webclient.components.search
  (:require
   ["@radix-ui/themes" :refer [IconButton]]
   ["lucide-react" :refer [Search]]
   [reagent.core :as r]
   [re-frame.core :as rf]))

(defn main []
  (let [has-text? (r/atom false)]
    (fn []
      (let [input-id "header-search"]
        [:div {:class "relative w-8 h-8"}
         [:input {:class (str "absolute top-0 right-0 "
                              "shadow-sm transition-all ease-in duration-150 "
                              "bg-gray-3 "
                              "text-sm h-8 "
                              (if @has-text? "w-64" "w-8")
                              " rounded-md "
                              "outline-none pl-3 "
                              "focus:outline-none "
                              "focus:w-64 "
                              "cursor-pointer "
                              "focus:cursor-text "
                              "focus:pl-3 focus:pr-8")
                  :id input-id
                  :placeholder "Search connections"
                  :name "header-search"
                  :autoComplete "off"
                  :on-change (fn [e]
                               (let [value (-> e .-target .-value)]
                                 (reset! has-text? (not (empty? value)))
                                 (rf/dispatch [:connections/set-filter value])))}]
         [:> IconButton
          {:class (str "absolute top-0 right-0 w-8 h-8 "
                       "bg-gray-3 hover:bg-gray-4")
           :size "2"
           :variant "soft"
           :color "gray"
           :onClick #(.focus (.getElementById js/document input-id))}
          [:> Search {:size 16}]]]))))
