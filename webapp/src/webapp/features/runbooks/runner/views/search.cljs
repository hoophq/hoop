(ns webapp.features.runbooks.runner.views.search
  (:require
   ["@radix-ui/themes" :refer [IconButton]]
   ["lucide-react" :refer [Search X]]
   [reagent.core :as r]
   [re-frame.core :as rf]))

(defn main []
  (let [has-text? (r/atom false)
        search-term (rf/subscribe [:search/term])
        active-panel (rf/subscribe [:webclient->active-panel])]
    (fn []
      (let [input-id "header-search"]
        (reset! has-text? (not (empty? @search-term)))

        ;; 24px box to sit on the 40px header's control scale, alongside the
        ;; ghost icon buttons next to it.
        [:div {:class "relative w-6 h-6"}
         [:input {:class (str "absolute top-0 right-0 "
                              " shadow-sm transition-all ease-in duration-150 "
                              " bg-gray-3 "
                              " text-sm h-6 "
                              (if @has-text? " w-64 " " w-6 ")
                              " rounded-md "
                              " outline-none pl-3 "
                              " focus:outline-none "
                              " focus:w-64 "
                              " cursor-pointer "
                              " focus:cursor-text "
                              ;; Keep the clear button's 24px clear of the text
                              ;; whenever the field is expanded, not only while
                              ;; it has focus — a filled-but-blurred field runs
                              ;; its own value underneath the X otherwise.
                              (if @has-text? " pr-8 " " focus:pr-8 ")
                              " dark:text-gray-12 ")
                  :id input-id
                  :placeholder "Search runbooks"
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
         ;; `soft`, not `ghost`, even though every other control on this bar is
         ;; ghost. Radix gives a ghost IconButton a negative margin equal to its
         ;; own padding (-4px at size 1) so it optically aligns with text in
         ;; normal flow; this one is absolutely positioned, so that margin just
         ;; shifts it 4px up and 4px right, off the input it is supposed to sit
         ;; on. Soft also keeps the collapsed state readable as a button.
         (if @has-text?
           [:> IconButton
            {:class " absolute top-0 right-0 w-6 h-6 bg-gray-3 hover:bg-gray-4 "
             :size "1"
             :variant "soft"
             :color "gray"
             :highContrast true
             :aria-label "Clear search"
             :onClick (fn [e]
                        (.stopPropagation e)

                        (set! (.-value (.getElementById js/document input-id)) "")
                        (rf/dispatch [:search/clear-term])

                        (if (= @active-panel :runbooks)
                          (rf/dispatch [:search/filter-runbooks ""])
                          (rf/dispatch [:primary-connection/set-filter ""])))}
            [:> X {:size 16}]]

           [:> IconButton
            {:class " absolute top-0 right-0 w-6 h-6 bg-gray-3 hover:bg-gray-4 "
             :size "1"
             :variant "soft"
             :color "gray"
             :highContrast true
             :aria-label "Search runbooks"
             :onClick #(.focus (.getElementById js/document input-id))}
            [:> Search {:size 16}]])]))))
