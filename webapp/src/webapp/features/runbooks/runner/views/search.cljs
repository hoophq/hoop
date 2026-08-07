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
         ;; The button's fill is state-dependent, and each state has one job.
         ;;
         ;; Collapsed, the button covers the whole 24px input, so its fill is
         ;; what the control looks like: bg-gray-4, the bar's own colour, so the
         ;; widget reads as part of the bar next to the ghost buttons instead of
         ;; as a lighter chip stuck on it. (The old constant bg-gray-3 was tuned
         ;; against the bg-gray-1 bar this PR replaced; against bg-gray-4 it is
         ;; lighter than its surface in the light theme and darker in the dark
         ;; one.) Hover has to move OFF the bar colour to register at all, hence
         ;; gray-5 — the previous hover:bg-gray-4 is now the rest state.
         ;;
         ;; Expanded, the button sits on the right edge of a 256px gray-3 field,
         ;; so any fill of its own is a seam. It goes transparent and lets the
         ;; field show through, and hovers to gray-4 for the affordance.
         ;;
         ;; `soft`, not `ghost`, in both states: ghost has no fixed box — Radix
         ;; sizes it from padding and pulls it back with a -4px margin on all
         ;; four sides — so pinned with top-0/right-0 it lands 4px off, and
         ;; forcing w-6/h-6 on it yields a 32px box because `all: unset` leaves
         ;; it content-box. Soft is a plain 24x24 border box at offset 0,0.
         ;; Tailwind's fills win over the soft variant's own background: equal
         ;; specificity, and the utilities are emitted ~680KB later in site.css.
         (if @has-text?
           [:> IconButton
            {:class " absolute top-0 right-0 w-6 h-6 bg-transparent hover:bg-gray-4 "
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
            {:class " absolute top-0 right-0 w-6 h-6 bg-gray-4 hover:bg-gray-5 "
             :size "1"
             :variant "soft"
             :color "gray"
             :highContrast true
             :aria-label "Search runbooks"
             :onClick #(.focus (.getElementById js/document input-id))}
            [:> Search {:size 16}]])]))))
