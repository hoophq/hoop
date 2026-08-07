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
                              ;; The field only paints a surface once it is
                              ;; expanded. Collapsed, this is a 24px icon and
                              ;; nothing else, exactly like the three ghost
                              ;; buttons beside it. A constant fill was tuned
                              ;; against the old bg-gray-1 bar: on today's
                              ;; bg-gray-4 bar gray-3 is LIGHTER in the light
                              ;; theme and DARKER in the dark one, so the chip
                              ;; had no consistent relationship to the surface
                              ;; it sat on, and it was the only filled control
                              ;; on the bar.
                              (if @has-text? " bg-gray-3 " " bg-transparent focus:bg-gray-3 ")
                              " text-sm h-6 "
                              (if @has-text? " w-64 " " w-6 ")
                              " rounded-md "
                              " outline-none pl-3 "
                              " focus:outline-none "
                              " focus:w-64 "
                              " cursor-pointer "
                              " focus:cursor-text "
                              ;; Reserve the button's 24px plus an 8px gap
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
         ;; The button carries no background of its own — it takes Radix's real
         ;; ghost tokens, so it is transparent at rest and picks up gray-a3 on
         ;; hover, identical to the theme/metadata buttons on this bar. Hand
         ;; matching a fill here is what produced the mismatch above, and the
         ;; hover literal it needed had drifted to exactly the bar's own colour,
         ;; which made hovering the collapsed button erase it.
         ;;
         ;; The wrapper is load-bearing. Radix sizes a ghost IconButton from its
         ;; padding and pulls it back with a -4px margin on all four sides so it
         ;; optically aligns with text in normal flow; pinned directly with
         ;; top-0/right-0 it lands 4px up and 4px right of the input, and
         ;; forcing w-6/h-6 on it makes a 32px box because `all: unset` leaves
         ;; it content-box. Centring it inside a fixed 24px flex box cancels the
         ;; margin exactly — measured at 24x24, offset 0,0 in both themes — and
         ;; lets the button keep its natural size. Same technique, and the same
         ;; reason, as webapp.components.notification-badge.
         (let [pin "absolute top-0 right-0 w-6 h-6 inline-flex items-center justify-center"]
           (if @has-text?
             [:span {:class pin}
              [:> IconButton
               {:size "1"
                :variant "ghost"
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
               [:> X {:size 16}]]]

             [:span {:class pin}
              [:> IconButton
               {:size "1"
                :variant "ghost"
                :color "gray"
                :highContrast true
                :aria-label "Search runbooks"
                :onClick #(.focus (.getElementById js/document input-id))}
               [:> Search {:size 16}]]]))]))))
