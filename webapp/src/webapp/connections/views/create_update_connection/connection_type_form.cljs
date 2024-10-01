(ns webapp.connections.views.create-update-connection.connection-type-form
  (:require ["@radix-ui/themes" :refer [Avatar RadioGroup Box Card Flex Grid Text]]
            ["lucide-react" :refer [Database SquareTerminal Workflow AppWindow]]
            [reagent.core :as r]
            [clojure.string :as str]))

(def connections-type
  [{:icon (r/as-element [:> Database {:size 16}])
    :title "Database"
    :subtitle "For PostgreSQL, MySQL, Microsoft SQL and more."
    :value :database}
   {:icon (r/as-element [:> SquareTerminal {:size 16}])
    :title "Shell"
    :subtitle "Custom connection for your services."
    :value :custom}
   {:icon (r/as-element [:> SquareTerminal {:size 16}])
    :title "SSH"
    :subtitle "Secure shell protocol for remote access."
    :value :ssh}
   {:icon (r/as-element [:> Workflow {:size 16}])
    :title "TCP"
    :subtitle "Transmission protocol for reliable transmission of data."
    :value :tcp}
   {:icon (r/as-element [:> AppWindow {:size 16}])
    :title "Application"
    :subtitle "For Ruby on Rails, Python, Node JS and more."
    :value :application}])

(def connections-subtypes
  {:database [{:value :postgres :title "PostgreSQL"}
              {:value :mysql :title "MySQL"}
              {:value :mssql :title "Microsoft SQL"}
              {:value :oracledb :title "Oracle DB"}
              {:value :mongodb :title "MongoDB"}]
   :custom []
   :ssh []
   :tcp []
   :application [{:value :ruby-on-rails :title "Ruby on Rails"}
                 {:value :python :title "Python"}
                 {:value :nodejs :title "Node JS"}
                 {:value :clojure :title "Clojure"}]})

(defn main [connection-type connection-subtype]
  [:> Flex {:direction "column" :gap "9" :class "px-20"}
   [:> Grid {:columns "2"}
    [:> Flex {:direction "column"}
     [:> Text {:size "4" :weight "bold"} "Connection type"]
     [:> Text {:size "3"} "Select the type of connection for your service."]]
    [:> Box {:class "space-y-radix-5"}
     (doall
      (for [{:keys [icon title subtitle value]} connections-type]
        (let [is-selected (= @connection-type value)]
          ^{:key title}
          [:> Card {:size "1"
                    :variant "surface"
                    :class (str "w-full cursor-pointer " (when is-selected "before:bg-primary-12"))
                    :on-click #(reset! connection-type value)}
           [:> Flex {:align "center" :gap "3"}
            [:> Avatar {:size "4"
                        :class (when is-selected "dark")
                        :variant "soft"
                        :color "gray"
                        :fallback icon}]
            [:> Flex {:direction "column" :class (str "" (when is-selected "text-gray-4"))}
             [:> Text {:size "3" :weight "medium" :color "gray-12"} title]
             [:> Text {:size "2" :color "gray-11"} subtitle]]]])))]]

   (when (seq (get connections-subtypes @connection-type))
     [:> Grid {:columns "2"}
      [:> Flex {:direction "column"}
       [:> Text {:size "4" :weight "bold"} (str (str/capitalize (name @connection-type))) " type"]
       [:> Text {:size "3"} (str "Select the type of " (name @connection-type) " for your connection.")]]
      [:> Box {:class "space-y-radix-5"}
       [:> RadioGroup.Root {:name (str (name @connection-type) "-type") :class "space-y-radix-4"}
        (doall
         (for [{:keys [value title]} (get connections-subtypes @connection-type)]
           ^{:key title}
           [:> RadioGroup.Item {:value value :onCheckedChange #(reset! connection-subtype value)} title]))]]])])
