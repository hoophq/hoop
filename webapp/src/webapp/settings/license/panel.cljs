(ns webapp.settings.license.panel
  (:require
   ["@radix-ui/themes" :refer [Box Button Callout Flex Heading Link Table Text]]
   ["lucide-react" :refer [AlertCircle]]
   [clojure.string :as strings]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.components.forms :as forms]
   [webapp.config :as config]))

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
      [:> Table.Root {:variant "surface"}
       [:> Table.Header
        [:> Table.Row
         [:> Table.ColumnHeaderCell "Type"]
         [:> Table.ColumnHeaderCell "Issued"]
         [:> Table.ColumnHeaderCell "Expiration"]]]
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
        [:> Heading {:size "4" :weight "bold" :as "h3" :class "text-gray-12"}
         "License Details"]]
       [:> Box
        [:> Table.Root {:variant "surface"}
         [:> Table.Body
          [:> Table.Row
           [:> Table.ColumnHeaderCell
            [:> Text {:size "1"} "Verified Hostname"]]
           [:> Table.Cell
            [:> Text (:verified_host license-info)]]]
          [:> Table.Row
           [:> Table.ColumnHeaderCell
            [:> Text {:size "1"} "Enterprise License"]]
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

(defn license-expiration-warning []
  (let [should-show-warning (rf/subscribe [:gateway->should-show-license-expiration-warning])]
    (fn []
      (when @should-show-warning
        [:> Box {:class "mb-6"}
         [:> Callout.Root {:color "yellow" :role "alert" :size "1"}
          [:> Callout.Icon
           [:> AlertCircle {:size 16 :class "text-warning-12"}]]
          [:> Callout.Text {:class "text-warning-12"}
           "Your organization's license is expiring soon. Please contact us to avoid interruption."]]]))))

(defn main []
  (let [gateway-info (rf/subscribe [:gateway->info])
        ;; used here for the input because the action
        ;; button stays in the top of the page
        license-value (r/atom "")]
    (fn []
      (let [license-info (-> @gateway-info :data :license_info)
            is-valid? (:is_valid license-info)
            license-type (:type license-info)
            disable-license-input? (and is-valid? (= license-type "enterprise"))]
        [:> Flex {:direction "column"
                  :class "h-full"}
         [:> Flex {:class "mb-10"
                   :as "header"}
          [:> Box {:flexGrow "1"}
           [:> Heading {:size "8" :weight "bold" :as "h1" :class "text-gray-12"}
            "License"]
           [:> Text {:size "2" :class "text-gray-11"}
            "View and manage your organization's license."]]
          [:> Flex {:gap "6" :align "center"}
           [:> Button {:size "3"
                       :variant "ghost"
                       :on-click #(js/window.open "https://help.hoop.dev/" "_blank")}
            "Contact us"]
           [:> Button {:size "3"
                       :disabled (or disable-license-input?
                                     (strings/blank? @license-value))
                       :on-click #(rf/dispatch [:license->update-license-key @license-value])}
            "Save"]]]

         [license-expiration-warning]

         [:> Flex {:direction :column
                   :gap "8"
                   :class "flex-1 overflow-auto"}
          [information-table license-info]
          [license-table {:license-info license-info
                          :license-value license-value
                          :disable-input? disable-license-input?}]

          [:> Text {:size "1" :class "text-gray-11 mt-auto self-center"}
           "Need more information? Check out "
           [:> Link {:href (get-in config/docs-url [:setup :license-management])
                     :target "_blank"}
            "License Management documentation"]
           " or "
           [:> Link {:href "https://help.hoop.dev/"
                     :target "_blank"}
            "contact us"]
           ". "]]]))))
