(ns webapp.webclient.components.search
  (:require
   ["@radix-ui/themes" :refer [IconButton]]
   ["lucide-react" :refer [Search X]]
   [reagent.core :as r]
   [re-frame.core :as rf]))

(defn main []
  (let [has-text? (r/atom false)
        search-term (rf/subscribe [:search/term])]
    (fn [active-panel]
      (let [input-id "header-search"]
        (reset! has-text? (not (empty? @search-term)))

        [:div {:class "relative w-8 h-8"}
         [:input {:class (str "absolute top-0 right-0 "
                              " shadow-sm transition-all ease-in duration-150 "
                              " bg-gray-3 "
                              " text-sm h-8 "
                              (if @has-text? " w-64 " " w-8 ")
                              " rounded-md "
                              " outline-none pl-3 "
                              " focus:outline-none "
                              " focus:w-64 "
                              " cursor-pointer "
                              " focus:cursor-text "
                              " focus:pl-3 focus:pr-8 "
                              " dark:text-gray-12 ")
                  :id input-id
                  :placeholder "Search connections"
                  :name "header-search"
                  :autoComplete "off"
                  :value @search-term
                  :on-change (fn [e]
                               (let [value (-> e .-target .-value)]
                                 (reset! has-text? (not (empty? value)))
                                 (rf/dispatch [:search/set-term value])
                                 (if (= @active-panel :runbooks)
                                   (rf/dispatch [:search/filter-runbooks value])
                                   (rf/dispatch [:primary-connection/set-filter value]))))}]
         (if @has-text?
           [:> IconButton
            {:class (str " absolute top-0 right-0 w-8 h-8 "
                         " bg-gray-3 hover:bg-gray-4 ")
             :size "2"
             :variant "soft"
             :color "gray"
             :onClick (fn [e]
                        (.stopPropagation e)

                        (set! (.-value (.getElementById js/document input-id)) "")
                        (rf/dispatch [:search/clear-term])

                        (if (= @active-panel :runbooks)
                          (rf/dispatch [:search/filter-runbooks ""])
                          (rf/dispatch [:primary-connection/set-filter ""])))}
            [:> X {:size 16}]]

           [:> IconButton
            {:class (str " absolute top-0 right-0 w-8 h-8 "
                         " bg-gray-3 hover:bg-gray-4 ")
             :size "2"
             :variant "soft"
             :color "gray"
             :onClick #(.focus (.getElementById js/document input-id))}
            [:> Search {:size 16}]])]))))
