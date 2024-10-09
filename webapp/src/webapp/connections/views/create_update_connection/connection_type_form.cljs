(ns webapp.connections.views.create-update-connection.connection-type-form
  (:require ["@radix-ui/themes" :refer [Avatar Box Card Flex Grid RadioGroup
                                        Text]]
            ["lucide-react" :refer [AppWindow Database SquareTerminal Workflow]]
            [clojure.string :as str]
            [reagent.core :as r]
            [webapp.connections.utilities :as utils]))

(def connections-type
  [{:icon (r/as-element [:> Database {:size 16}])
    :title "Database"
    :subtitle "For PostgreSQL, MySQL, Microsoft SQL and more."
    :value "database"}
   {:icon (r/as-element [:> SquareTerminal {:size 16}])
    :title "Shell"
    :subtitle "Custom connection for your services."
    :value "custom"}
   {:icon (r/as-element [:> SquareTerminal {:size 16}])
    :title "SSH"
    :subtitle "Secure shell protocol for remote access."
    :value "ssh"}
   {:icon (r/as-element [:> Workflow {:size 16}])
    :title "TCP"
    :subtitle "Transmission protocol for reliable transmission of data."
    :value "tcp"}
   {:icon (r/as-element [:> AppWindow {:size 16}])
    :title "Application"
    :subtitle "For Ruby on Rails, Python, Node JS and more."
    :value "application"}])

(def connections-subtypes
  {"database" [{:value :postgres :title "PostgreSQL"}
               {:value :mysql :title "MySQL"}
               {:value :mssql :title "Microsoft SQL"}
               {:value :oracledb :title "Oracle DB"}
               {:value :mongodb :title "MongoDB"}]
   "custom" []
   "application" [{:value :ruby-on-rails :title "Ruby on Rails"}
                  {:value :python :title "Python"}
                  {:value :nodejs :title "Node JS"}
                  {:value :clojure :title "Clojure"}]})

(defn set-connection-type-context
  [value
   connection-type
   connection-subtype
   config-file-name
   database-schema?]
  (cond
    (= value "database") (do (reset! connection-type "database")
                             (reset! connection-subtype nil)
                             (reset! database-schema? true))

    (= value "custom") (do (reset! connection-type "custom")
                           (reset! connection-subtype nil))

    (= value "ssh") (do (reset! connection-type "custom")
                        (reset! connection-subtype "ssh")
                        (reset! config-file-name "SSH_PRIVATE_KEY"))

    (= value "tcp") (do (reset! connection-type "application")
                        (reset! connection-subtype "tcp"))

    (= value "application") (do (reset! connection-type "application")
                                (reset! connection-subtype nil))))

(defn is-connection-type-selected [value connection-type connection-subtype]
  (cond
    (= value "database") (= value connection-type)

    (= value "custom") (and (= value connection-type)
                            (not= connection-subtype "ssh"))

    (= value "ssh") (and (= value connection-subtype)
                         (= connection-type "custom"))

    (= value "tcp") (and (= value connection-subtype)
                         (= connection-type "application"))

    (= value "application") (and (= value connection-type)
                                 (not= connection-subtype "tcp"))))

(defn main [{:keys [connection-type connection-subtype configs config-file-name database-schema?]}]
  [:> Flex {:direction "column" :gap "9" :class "px-20"}
   [:> Grid {:columns "5" :gap "7"}
    [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
     [:> Text {:size "4" :weight "bold" :class "text-gray-12"} "Connection type"]
     [:> Text {:size "3" :class "text-gray-11"} "Select the type of connection for your service."]]
    [:> Box {:class "space-y-radix-5" :grid-column "span 3 / span 3"}
     (doall
      (for [{:keys [icon title subtitle value]} connections-type]
        (let [is-selected (is-connection-type-selected value @connection-type @connection-subtype)]
          ^{:key title}
          [:> Card {:size "1"
                    :variant "surface"
                    :class (str "w-full cursor-pointer " (when is-selected "before:bg-primary-12"))
                    :on-click (fn [_]
                                (set-connection-type-context
                                 value
                                 connection-type
                                 connection-subtype
                                 config-file-name
                                 database-schema?)
                                (reset! configs (utils/get-config-keys (keyword value))))}
           [:> Flex {:align "center" :gap "3"}
            [:> Avatar {:size "4"
                        :class (when is-selected "dark")
                        :variant "soft"
                        :color "gray"
                        :fallback icon}]
            [:> Flex {:direction "column" :class (str "" (when is-selected "text-gray-4"))}
             [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
             [:> Text {:size "2" :color "gray-11"} subtitle]]]])))]]

   (when (and (seq (get connections-subtypes @connection-type))
              (not= @connection-subtype "tcp"))
     [:> Grid {:columns "5" :gap "7"}
      [:> Flex {:direction "column" :grid-column "span 2 / span 2"}
       [:> Text {:size "4" :weight "bold" :class "text-gray-12"} (str (str/capitalize (name @connection-type))) " type"]
       [:> Text {:size "3" :class "text-gray-11"} (str "Select the type of " (name @connection-type) " for your connection.")]]
      [:> Box {:class "space-y-radix-5" :grid-column "span 3 / span 3"}
       [:> RadioGroup.Root {:name (str (name @connection-type) "-type")
                            :class "space-y-radix-4"
                            :value @connection-subtype
                            :on-value-change (fn [value]
                                               (reset! connection-subtype value)
                                               (reset! configs (utils/get-config-keys (keyword value))))}
        (doall
         (for [{:keys [value title]} (get connections-subtypes @connection-type)]
           ^{:key title}
           [:> RadioGroup.Item {:value value} title]))]]])])
