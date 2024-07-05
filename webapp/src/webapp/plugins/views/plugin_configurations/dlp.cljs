(ns webapp.plugins.views.plugin-configurations.dlp
  (:require [clojure.string :as cs]
            [re-frame.core :as rf]
            [reagent.core :as r]
            [webapp.components.button :as button]
            [webapp.components.multiselect :as multi-select]
            [webapp.plugins.views.plugin-configurations.container :as plugin-configuration-container]))

(def info-types-options
  ["PHONE_NUMBER",
   "CREDIT_CARD_NUMBER",
   "CREDIT_CARD_TRACK_NUMBER",
   "EMAIL_ADDRESS",
   "IBAN_CODE",
   "HTTP_COOKIE",
   "IMEI_HARDWARE_ID",
   "IP_ADDRESS",
   "STORAGE_SIGNED_URL",
   "URL",
   "VEHICLE_IDENTIFICATION_NUMBER",
   "BRAZIL_CPF_NUMBER",
   "AMERICAN_BANKERS_CUSIP_ID",
   "FDA_CODE",
   "US_PASSPORT",
   "US_SOCIAL_SECURITY_NUMBER"])

(defn array->select-options [array]
  (mapv #(into {} {:value % :label (cs/lower-case (cs/replace % #"_" " "))}) array))

(defn js-select-options->list [options]
  (mapv #(get % "value") options))

(defn configuration-modal [_]
  (let [info-types-groups-atom (r/atom [])]
    (fn [{:keys [connection plugin]}]
      (let [current-connection-config (first (filter #(= (:id connection)
                                                         (:id %))
                                                     (:connections plugin)))
            info-types-groups-value (if (empty? @info-types-groups-atom)
                                      (array->select-options (:config current-connection-config))
                                      @info-types-groups-atom)]
        [:section {:class "flex flex-col px-small pt-regular"}
         [:form
          {:on-submit (fn [e]
                        (.preventDefault e)
                        (let [connection (merge current-connection-config
                                                {:config (js-select-options->list
                                                          @info-types-groups-atom)})
                              dissoced-connections (filter #(not= (:id %)
                                                                  (:id connection))
                                                           (:connections plugin))
                              new-plugin-data (assoc plugin :connections (conj
                                                                          dissoced-connections
                                                                          connection))]
                          (rf/dispatch [:plugins->update-plugin new-plugin-data])))}
          [:header
           [:div {:class "font-bold text-xs"}
            "Info types"]
           [:section
            {:class "text-xs text-gray-600 pb-1"}
            (str "Comma separated values of types of information that will be redacted and masked. ")
            [:a {:class "text-blue-500"
                 :href "https://cloud.google.com/dlp/docs/infotypes-reference"}
             "Click here to found more info types accepted."]]]
          [:div {:class "mb-4"}
           [multi-select/main {:options (array->select-options info-types-options)
                               :default-value info-types-groups-value
                               :on-change #(reset! info-types-groups-atom (js->clj %))}]]

          [:footer
           {:class "flex justify-end"}
           [:div {:class "flex-shrink"}
            [button/primary {:text "Save"
                             :variant :small
                             :type "submit"}]]]]]))))

(defn main []
  [plugin-configuration-container/main configuration-modal])
