(ns webapp.events.reports
  (:require  [cljs-time.core :as time]
             [cljs-time.format :as fmt]
             [cljs-time.coerce :as coerce]
             [clojure.string :as cs]
             [re-frame.core :as rf]))

(defn suffix [day]
  (cond
    (or (= day 1) (= day 21) (= day 31)) "st"
    (or (= day 2) (= day 22)) "nd"
    (or (= day 3) (= day 23)) "rd"
    :else "th"))

(defn format-date-with-suffix [date]
  (let [month-str (fmt/unparse (fmt/formatter "MMM") date)
        day (time/day date)
        day-with-suffix (str day (suffix day))]
    (str month-str " " day-with-suffix)))

(rf/reg-event-fx
 :reports->get-report-by-session-id
 (fn
   [{:keys [db]} [_ session]]
   {:db (assoc db :reports->session {:status :loading
                                     :data nil})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri (str "/reports/sessions?id=" (:id session) "&start_date="
                                (first (cs/split (:start_date session) #"T")))
                      :on-success #(rf/dispatch [::reports->set-report-by-session-id %])}]]]}))

(rf/reg-event-fx
 :reports->clear-session-report-by-id
 (fn [{:keys [db]} [_]]
   {:db (assoc db :reports->session {:status :loading
                                     :data nil})}))


(rf/reg-event-fx
 ::reports->set-report-by-session-id
 (fn
   [{:keys [db]} [_ report]]
   {:db (assoc db :reports->session {:status :ready
                                     :data report})}))



(rf/reg-event-fx
 :reports->get-redacted-data-by-date
 (fn
   [{:keys [db]} [_ days]]
   (let [end-date (time/today)
         start-date (time/minus end-date (time/days days))
         date-format (fmt/formatter "yyyy-MM-dd")
         query-params (if (= days 1)
                        (str "start_date=" (fmt/unparse date-format (time/today)))
                        (str "start_date=" (fmt/unparse date-format start-date)
                             "&end_date=" (fmt/unparse date-format end-date)))]
     {:db (assoc db :reports->redacted-data-by-date {:status :loading
                                                     :data nil})
      :fx [[:dispatch [:fetch
                       {:method "GET"
                        :uri (str "/reports/sessions?" query-params)
                        :on-success #(rf/dispatch [::reports->set-redacted-data % days])}]]]})))

(rf/reg-event-fx
 ::reports->set-redacted-data
 (fn
   [{:keys [db]} [_ report days]]
   {:db (assoc db :reports->redacted-data-by-date {:status :ready
                                                   :data (merge report
                                                                {:range-date
                                                                 (str (format-date-with-suffix (time/minus (time/today) (time/days days)))
                                                                      " - "
                                                                      (format-date-with-suffix (time/today)))})})}))

(rf/reg-event-fx
 :reports->get-today-redacted-data
 (fn
   [{:keys [db]} [_]]
   (let [start-date (time/today)
         date-format (fmt/formatter "yyyy-MM-dd")
         query-params (str "start_date=" (fmt/unparse date-format start-date))]
     {:db (assoc db :reports->today-redacted-data {:status :loading
                                                   :data nil})
      :fx [[:dispatch [:fetch
                       {:method "GET"
                        :uri (str "/reports/sessions?" query-params)
                        :on-success #(rf/dispatch [::reports->set-today-redacted-data %])}]]]})))

(rf/reg-event-fx
 ::reports->set-today-redacted-data
 (fn
   [{:keys [db]} [_ report]]
   {:db (assoc db :reports->today-redacted-data {:status :ready
                                                 :data report})}))

(rf/reg-event-fx
 :reports->get-review-data-by-date
 (fn
   [{:keys [db]} [_ days]]
   {:db (assoc db :reports->review-data-by-date {:status :loading
                                                 :data nil})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/reviews"
                      :on-success #(rf/dispatch [::reports->set-review-data-by-date % days])}]]]}))

(rf/reg-event-fx
 ::reports->set-review-data-by-date
 (fn
   [{:keys [db]} [_ report days]]
   (let [parse-date (fn [date-str]
                      (let [date (.toISOString
                                  (new js/Date date-str))]
                        (coerce/from-string date)))
         filter-by-days (fn [reviews days]
                          (let [cutoff-date (time/minus (time/now) (time/days days))]
                            (filter #(time/after? (parse-date (:created_at %)) cutoff-date) reviews)))]
     {:db (assoc db :reports->review-data-by-date {:status :ready
                                                   :data (merge
                                                          {:results (filter-by-days report days)}
                                                          {:range-date (str (format-date-with-suffix (time/minus (time/today) (time/days days)))
                                                                            " - "
                                                                            (format-date-with-suffix (time/today)))})})})))

(rf/reg-event-fx
 :reports->get-today-review-data
 (fn
   [{:keys [db]} [_ days]]
   {:db (assoc db :reports->today-review-data {:status :loading
                                               :data nil})
    :fx [[:dispatch [:fetch
                     {:method "GET"
                      :uri "/reviews"
                      :on-success #(rf/dispatch [::reports->set-today-review-data % days])}]]]}))

(rf/reg-event-fx
 ::reports->set-today-review-data
 (fn
   [{:keys [db]} [_ report]]
   (let [parse-date (fn [date-str]
                      (let [date (.toISOString
                                  (new js/Date date-str))]
                        (fmt/parse (fmt/formatters :date-time) date)))
         filter-by-days (fn [reviews]
                          (let [today-start (time/today-at-midnight)
                                tomorrow-start (time/plus today-start (time/days 1))]
                            (filter #(and (time/after? (parse-date (:created_at %)) today-start)
                                          (time/before? (parse-date (:created_at %)) tomorrow-start))
                                    reviews)))]
     {:db (assoc db :reports->today-review-data {:status :ready
                                                 :data (filter-by-days report)})})))

(rf/reg-event-fx
 :reports->get-today-session-data
 (fn
   [{:keys [db]} [_]]
   (let [start-date (time/today-at-midnight)
         end-date (time/plus start-date (time/hours 23) (time/minutes 59) (time/seconds 59))
         query-params (str "start_date=" (fmt/unparse (fmt/formatters :date-time) start-date)
                           "&end_date="  (fmt/unparse (fmt/formatters :date-time) end-date))]
     {:db (assoc db :reports->today-session-data {:status :loading
                                                  :data nil})
      :fx [[:dispatch [:fetch {:method "GET"
                               :uri (str "/sessions?" query-params)
                               :on-success #(rf/dispatch [::reports->set-today-session-data %])}]]]})))

(rf/reg-event-fx
 ::reports->set-today-session-data
 (fn
   [{:keys [db]} [_ sessions]]
   {:db (assoc db :reports->today-session-data {:status :ready
                                                :data sessions})}))
