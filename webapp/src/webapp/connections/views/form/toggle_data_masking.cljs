(ns webapp.connections.views.form.toggle-data-masking
  (:require ["@heroicons/react/16/solid" :as hero-micro-icon]
            [clojure.string :as cs]
            [webapp.components.headings :as h]
            [webapp.components.multiselect :as multi-select]
            [webapp.components.toggle :as toggle]))
(def dlp-info-types-options
  ["PHONE_NUMBER",
   "CREDIT_CARD_NUMBER",
   "AUTH_TOKEN",
   "AWS_CREDENTIALS",
   "AZURE_AUTH_TOKEN",
   "BASIC_AUTH_HEADER",
   "ENCRYPTION_KEY",
   "GCP_API_KEY",
   "GCP_CREDENTIALS",
   "JSON_WEB_TOKEN",
   "HTTP_COOKIE",
   "OAUTH_CLIENT_SECRET",
   "PASSWORD",
   "SSL_CERTIFICATE",
   "STORAGE_SIGNED_POLICY_DOCUMENT",
   "STORAGE_SIGNED_URL",
   "WEAK_PASSWORD_HASH",
   "XSRF_TOKEN",
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
  (mapv #(into {} {"value" % "label" (cs/lower-case (cs/replace % #"_" " "))}) array))


(defn main [{:keys [free-license?
                    data-masking-toggle-enabled?
                    data-masking-groups-value]}]
  [:div {:class ""}
   [:div {:class "mb-regular flex justify-between items-center"}
    [:div {:class "mr-24"}
     [:div {:class "flex items-center gap-2"}
      [:> hero-micro-icon/SparklesIcon {:class "h-6 w-6 text-purple-400"
                                        :aria-hidden "true"}]
      [:div {:class "flex items-center gap-2"}
       [h/h4-md "Enable AI Data Masking"
        {:class (when free-license? "text-opacity-70")}]
       (when free-license?
         [:div {:class "text-blue-600 bg-blue-600 bg-opacity-10 rounded-md px-2 py-1 cursor-pointer"
                :on-click #(js/window.Intercom
                            "showNewMessage"
                            "I want to upgrade my current plan")}
          "Upgrade to Pro"])]]
     [:label {:class "text-xs text-gray-500"}
      "Automagically avoid showing sensitive data with our AI for Data Masking"]]
    [toggle/main {:enabled? @data-masking-toggle-enabled?
                  :disabled? free-license?
                  :on-click (fn []
                              (reset! data-masking-toggle-enabled?
                                      (not @data-masking-toggle-enabled?)))}]]
   (when @data-masking-toggle-enabled?
     [multi-select/main {:options (array->select-options dlp-info-types-options)
                         :id "data-masking-groups-input"
                         :name "data-masking-groups-input"
                         :disabled? (or (not @data-masking-toggle-enabled?) free-license?)
                         :required? @data-masking-toggle-enabled?
                         :default-value (if @data-masking-toggle-enabled?
                                          @data-masking-groups-value
                                          nil)
                         :on-change #(reset! data-masking-groups-value (js->clj %))}])])
