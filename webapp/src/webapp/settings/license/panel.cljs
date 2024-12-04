(ns webapp.settings.license.panel
  (:require
    [clojure.string :as strings]
    [re-frame.core :as rf]
    [reagent.core :as r]
    ["@radix-ui/themes" :refer [Table Flex Box Text Heading Button]]
    [webapp.components.headings :as h]
    [webapp.components.forms :as forms]))

(defmulti license-status-text identity)
(defmethod license-status-text "oss" [_]
  "Open Source License")
(defmethod license-status-text "enterprise" [_]
  "Enterprise License")
(defmethod license-status-text :default [_]
  "Loading...")
(def months {0 "Jan", 1 "Feb", 2 "Mar", 3 "Apr", 4 "May", 5 "Jun", 6 "Jul", 7 "Aug", 8 "Sep", 9 "Oct", 10 "Nov", 11 "Dec"})

(defn get-formatted-date [timestamp]
  (let [date (new js/Date (* timestamp 1000))
        day (.getDate date)]
    (str (.getFullYear date)
         "/"
         (get months (.getMonth date))
         "/"
         (when (< day 10) "0")
         (.getDate date))))

(defn information-table [license-info]
  (let [license-type (:type license-info)
        issued-date (:issued_at license-info)
        expiration-date (:expire_at license-info)]
    [:> Flex {:direction :column
              :gap "4"}
     [:> Box
      [:> Heading {:size "5"}
       "Information"]]
     [:> Box
      [:> Table.Root {:variant "surface"}
       [:> Table.Header
        [:> Table.Row
         [:> Table.ColumnHeaderCell "Type"]
         [:> Table.ColumnHeaderCell "Issued"]
         [:> Table.ColumnHeaderCell "Expiration"]
         ]]
       [:> Table.Body
        [:> Table.Row
         [:> Table.Cell
          (license-status-text license-type)]
         [:> Table.Cell
          [:> Text {:size "1"}
           (get-formatted-date issued-date)]]
         [:> Table.Cell
          [:> Text {:size "1"}
           (cond
             (= license-type "oss") [:> Text {:color :gray} "N/A"]
             (= license-type "enterprise") (get-formatted-date expiration-date)
             :else "Loading...")]]]]]]]))

(defn license-table []
  (fn [{:keys [license-info license-value disable-input?]}]
    (let [key-id (:key_id license-info)]
      [:> Flex {:direction :column
                :gap "4"}
       [:> Box
        [:> Heading {:size "5" :as "h3"}
         "License"]]
       [:> Box
        [:> Table.Root {:variant "surface"}
         [:> Table.Body
          [:> Table.Row
           [:> Table.ColumnHeaderCell
            [:> Text {:size "1"} "Verified hostname"]]
           [:> Table.Cell
            [:> Text (:verified_host license-info)]]]
          [:> Table.Row
           [:> Table.ColumnHeaderCell
            [:> Text {:size "1"} "Key ID"]]
           [:> Table.Cell
            (if (empty? key-id)
              [:> Text {:size "1"
                        :color "gray"} "N/A"]
              [:> Text key-id])]]
          [:> Table.Row {:align "center"}
           [:> Table.ColumnHeaderCell
            [:> Text {:size "1" :as :div} "License Key"]]
           [:> Table.Cell {:align "right"}
            [forms/input {:value (if disable-input?
                                   "•••••••••••••••••"
                                   @license-value)
                          :on-change #(reset! license-value (-> % .-target .-value))
                          :disabled disable-input?
                          :placeholder "Enter your license key"
                          :type "password"}]]]]]]])))

(defn main []
  (let [gateway-info (rf/subscribe [:gateway->info])
        ;; used here for the input because the action
        ;; button stays in the top of the page
        license-value (r/atom "")]
    (fn []
      (let [license-info (-> @gateway-info :data :license_info)
            is-valid? (:is_valid license-info)
            license-type (:type license-info)
            disable-license-input? (and is-valid? (= license-type "enterprise")) ]
        [:div
         [:> Flex {:class "mb-10"
                   :as "header"}
          [:> Box {:flexGrow "1"}
           [h/PageHeader {:text "License Management"}]
           [:> Text {:size "5" :class "text-[--gray-11]"}
            "Manage your organization's license"]]
          [:> Box
           [:> Button {:size "3"
                       :disabled (or disable-license-input?
                                     (strings/blank? @license-value))
                       :on-click #(rf/dispatch [:license->update-license-key @license-value])}
            "Save license"]]]
         [:> Flex {:direction :column
                   :gap "8"}
          [information-table license-info]
          [license-table {:license-info license-info
                          :license-value license-value
                          :disable-input? disable-license-input?}]]]))))
