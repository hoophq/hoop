(ns webapp.audit-logs.filters
  (:require
   ["@heroicons/react/16/solid" :as hero-micro-icon]
   ["@radix-ui/themes" :refer [Button Popover]]
   ["react-tailwindcss-datepicker" :as Datepicker]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.audit-logs.events]
   [webapp.audit-logs.subs]
   [clojure.string :as cs]))

(defn date-filter []
  (let [filters (rf/subscribe [:audit-logs/filters])
        date (r/atom #js{"startDate" (or (:created-after @filters) "")
                         "endDate" (or (:created-before @filters) "")})]
    (fn []
      (let [dispatch-date (fn [date-value]
                            (let [start-date (.-startDate date-value)
                                  end-date (.-endDate date-value)
                                  iso-date (fn [filter-key date]
                                             (when (and date (not (string/blank? date)))
                                               (.toISOString
                                                (new js/Date
                                                     (if (= filter-key "start_date")
                                                       (str date " 00:00:00.000Z")
                                                       (str date " 23:59:59.000Z"))))))]
                              (rf/dispatch [:audit-logs/set-filters
                                            {:created-after (iso-date "start_date" start-date)
                                             :created-before (iso-date "end_date" end-date)}])))]

        [:> Datepicker {:value @date
                        :placeholder "Period"
                        :separator "-"
                        :displayFormat "DD/MM/YYYY"
                        :containerClassName "relative w-56 text-gray-11"
                        :toggleClassName (str "absolute rounded-l-lg "
                                              "text-gray-11 "
                                              "left-0 h-full px-3 "
                                              "focus:outline-none disabled:opacity-40 "
                                              "disabled:cursor-not-allowed")
                        :inputClassName (str (if (not
                                                  (or (cs/blank? (.-startDate @date))
                                                      (cs/blank? (.-endDate @date))))
                                               " bg-[--gray-a3] hover:bg-[--gray-a4] "
                                               " border border-gray-8 ")
                                             "pl-10 py-2 w-full rounded-lg text-gray-11 "
                                             "font-semibold text-sm focus:ring-0 "
                                             "h-[32px] "
                                             "placeholder:text-gray-500 "
                                             "hover:bg-gray-50 hover:text-gray-11 "
                                             "focus:bg-gray-50 focus:text-gray-11 focus:border-gray-400")
                        :useRange false
                        :showShortcuts true
                        :onChange (fn [v]
                                    (reset! date v)
                                    (dispatch-date v))}]))))

(defn user-filter []
  (let [filters (rf/subscribe [:audit-logs/filters])
        users (rf/subscribe [:users])
        search-term (r/atom "")]
    (fn []
      (let [active-user (:actor-email @filters)
            active? (not (string/blank? active-user))
            admin-users (filter #(some (fn [g] (= "admin" g)) (:groups %)) @users)
            filtered-users (if (string/blank? @search-term)
                             admin-users
                             (filter #(string/includes?
                                       (string/lower-case (:email %))
                                       (string/lower-case @search-term))
                                     admin-users))]
        [:> Popover.Root
         [:> Popover.Trigger {:asChild true}
          [:> Button {:size "2"
                      :variant (if active? "soft" "outline")
                      :color "gray"
                      :on-click #(reset! search-term "")}
           [:> (.-UserIcon hero-micro-icon) {:class "w-4 h-4"}]
           [:span {:class "text-sm font-semibold"}
            (if active?
              active-user
              "User")]
           (when active?
             [:div {:class "flex items-center justify-center rounded-full h-4 w-4 bg-gray-800"}
              [:span {:class "text-white text-xxs font-bold"} "1"]])]]

         [:> Popover.Content {:size "2" :style {:width "384px" :max-height "384px"}}
          [:div {:class "w-full max-h-96 overflow-y-auto"}
           (when active?
             [:div {:class "mb-2 pb-2 border-b border-gray-200"}
              [:div {:class "flex cursor-pointer items-center gap-2 text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2"
                     :on-click (fn []
                                 (rf/dispatch [:audit-logs/set-filters {:actor-email nil}]))}
               [:span "Clear filter"]]])

           [:div {:class "mb-2"}
            [:input {:type "text"
                     :class "w-full px-3 py-2 border border-gray-300 rounded-md text-sm"
                     :placeholder "Search users"
                     :value @search-term
                     :on-change #(reset! search-term (-> % .-target .-value))}]]

           (if (empty? filtered-users)
             [:div {:class "px-3 py-4 text-xs text-gray-700 italic"}
              "No users found"]

             [:ul
              (doall
               (for [user (sort-by :email filtered-users)]
                 ^{:key (:id user)}
                 [:li {:class "flex justify-between cursor-pointer items-center gap-2 text-sm text-gray-700 hover:bg-gray-200 rounded-md px-3 py-2"
                       :on-click (fn []
                                   (rf/dispatch [:audit-logs/set-filters
                                                 {:actor-email (if (= (:email user) active-user)
                                                                 nil
                                                                 (:email user))}]))}
                  [:span {:class "block truncate"} (:email user)]
                  (when (= (:email user) active-user)
                    [:> (.-CheckIcon hero-micro-icon) {:class "w-4 h-4 text-black"}])]))])]]]))))

(defn main []
  [:div {:class "flex gap-3"}
   [date-filter]
   [user-filter]])
